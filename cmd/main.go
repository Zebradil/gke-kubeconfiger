package cmd

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"os"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	cnt "google.golang.org/api/container/v1"
	su "google.golang.org/api/serviceusage/v1"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const KubeconfigBaseTemplate = `
{{- $longID := printf "gke_%s_%s_%s" .ProjectID .Location .ClusterName -}}
---
apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: {{ .CertificateAuthorityData }}
    server: {{ .Server }}
  name: {{ $longID }}
contexts:
- context:
    cluster: {{ $longID }}
    user: {{ $longID }}
  name: <CONTEXT_NAME>
preferences: {}
users:
- name: {{ $longID }}
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: gke-gcloud-auth-plugin
      installHint:
        Install gke-gcloud-auth-plugin for use with kubectl by following
        https://cloud.google.com/kubernetes-engine/docs/how-to/cluster-access-for-kubectl#install_plugin
      provideClusterInfo: true
`

const longDescription = `gke-kubeconfiger discovers GKE clusters and generates kubeconfig files for them.`

var cfgFile string

func init() {
	cobra.OnInitialize(initConfig)
}

func initConfig() {
	viper.SetEnvPrefix("GKEKC")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()

	if cfgFile == "" {
		cfgFile = viper.GetString("config")
	}

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".gke-kubeconfiger")
	}

	if err := viper.ReadInConfig(); err == nil {
		log.Info("Using config file:", viper.ConfigFileUsed())
	}
}

func NewRootCmd(version, commit, date string) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:     "gke-kubeconfiger",
		Short:   "Discovers GKE clusters and generates kubeconfig files for them.",
		Long:    longDescription,
		Args:    cobra.NoArgs,
		Version: fmt.Sprintf("%s, commit %s, built at %s", version, commit, date),
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			level, err := log.ParseLevel(viper.GetString("log-level"))
			if err != nil {
				return err
			}
			log.Info(cmd.Name(), " version ", cmd.Version)
			log.Info("Setting log level to:", level)
			log.SetLevel(level)
			return nil
		},
		Run: run,
	}

	rootCmd.
		PersistentFlags().
		StringVar(&cfgFile, "config", "", "config file (default is $HOME/.gke-kubeconfiger.yaml)")

	rootCmd.
		Flags().
		StringSlice("projects", []string{}, "Projects to filter by")

	rootCmd.
		Flags().
		Bool("rename", false, "Rename kubeconfig contexts")

	rootCmd.
		Flags().
		String("rename-tpl", "{{ .ProjectID }}/{{ .Location }}/{{ .ClusterName }}", "Rename template")

	rootCmd.
		Flags().
		String("log-level", "info", "Sets logging level: trace, debug, info, warning, error, fatal, panic")

	rootCmd.
		Flags().
		Int("batch-size", 10, "Batch size")

	err := viper.BindPFlags(rootCmd.Flags())
	if err != nil {
		log.WithError(err).Fatal("Couldn't bind flags")
	}

	return rootCmd
}

func run(cmd *cobra.Command, args []string) {
	if viper.ConfigFileUsed() != "" {
		log.WithField("config", viper.ConfigFileUsed()).Debug("Using config file")
	} else {
		log.Debug("No config file used")
	}

	batchSize := viper.GetInt("batch-size")
	preselectedProjects := viper.GetStringSlice("projects")
	rename := viper.GetBool("rename")
	renameTpl := viper.GetString("rename-tpl")

	contextNameTpl := "{{ $longID }}"
	if rename {
		contextNameTpl = renameTpl
	}

	kubeconfigTemplate, err := template.New("kubeconfig").Parse(strings.ReplaceAll(KubeconfigBaseTemplate, "<CONTEXT_NAME>", contextNameTpl))
	if err != nil {
		log.Fatalf("Failed to parse kubeconfig template: %v", err)
	}

	projects := make(chan string, batchSize)
	filteredProjects := make(chan string, batchSize)
	completed := make(chan bool)

	if len(preselectedProjects) > 0 {
		for _, project := range preselectedProjects {
			projects <- project
		}
		close(projects)
	} else {
		go getProjects(projects)
	}

	go filterProjects(projects, filteredProjects)
	go getCredentials(filteredProjects, kubeconfigTemplate, completed)

	for range completed {
	}
}

func getProjects(out chan<- string) {
	ctx := context.Background()
	crmService, err := crm.NewService(ctx)
	if err != nil {
		log.Fatalf("Failed to create cloudresourcemanager service: %v", err)
	}
	projects, err := crmService.Projects.List().Do()
	if err != nil {
		log.Fatalf("Failed to list projects: %v", err)
	}
	for _, project := range projects.Projects {
		out <- project.ProjectId
	}
	close(out)
}

func filterProjects(in <-chan string, out chan<- string) {
	ctx := context.Background()
	suService, err := su.NewService(ctx)
	if err != nil {
		log.Fatalf("Failed to create serviceusage service: %v", err)
	}
	suServicesService := su.NewServicesService(suService)
	wg := sync.WaitGroup{}
	for project := range in {
		wg.Add(1)
		go func(project string) {
			fmt.Printf("ProjectID: %s\n", project)
			containerServiceRes, err := suServicesService.Get(fmt.Sprintf("projects/%s/services/container.googleapis.com", project)).Do()
			if err != nil {
				log.Fatalf("Failed to get container service: %v", err)
			}
			if containerServiceRes.State == "ENABLED" {
				out <- project
			}
			wg.Done()
		}(project)
	}
	wg.Wait()
	close(out)
}

func getCredentials(in <-chan string, kubeconfigTemplate *template.Template, completed chan<- bool) {
	ctx := context.Background()
	containerService, err := cnt.NewService(ctx)
	if err != nil {
		log.Fatalf("Failed to create container service: %v", err)
	}
	wg := sync.WaitGroup{}
	for project := range in {
		wg.Add(1)
		go func(project string) {
			clusters, err := containerService.Projects.Locations.Clusters.List(fmt.Sprintf("projects/%s/locations/-", project)).Do()
			if err != nil {
				log.Fatalf("Failed to list clusters: %v", err)
			}
			for _, cluster := range clusters.Clusters {
				wg.Add(1)
				go func(cluster *cnt.Cluster) {
					fmt.Printf("Cluster: %s (%s)\n", cluster.Name, cluster.Location)
					endpoint := fmt.Sprintf("https://%s", cluster.Endpoint)
					cert := cluster.MasterAuth.ClusterCaCertificate
					kubeconfig := &bytes.Buffer{}
					err = kubeconfigTemplate.Execute(kubeconfig, map[string]string{
						"CertificateAuthorityData": cert,
						"Server":                   endpoint,
						"ProjectID":                project,
						"Location":                 cluster.Location,
						"ClusterName":              cluster.Name,
					})
					if err != nil {
						log.Fatalf("Failed to execute kubeconfig template: %v", err)
					}
					filename := fmt.Sprintf("%s_%s_%s.yaml", project, cluster.Location, cluster.Name)
					out, err := os.Create(filename)
					if err != nil {
						log.Fatalf("Failed to create file: %v", err)
					}
					defer out.Close()
					_, err = io.Copy(out, kubeconfig)
					if err != nil {
						log.Fatalf("Failed to write file: %v", err)
					}
					wg.Done()
				}(cluster)
			}
			wg.Done()
		}(project)
	}
	wg.Wait()
	close(completed)
}
