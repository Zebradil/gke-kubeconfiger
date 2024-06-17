package cmd

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	yaml "gopkg.in/yaml.v3"

	log "github.com/sirupsen/logrus"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	cnt "google.golang.org/api/container/v1"
	su "google.golang.org/api/serviceusage/v1"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Name of the user to use in kubeconfig entries.
// The same user configuration is used for all clusters.
const userName = "gke-kubeconfiger"

type programConfig struct {
	AuthPlugin     string
	BatchSize      int
	ConfigFile     string
	DestDir        string
	KubeconfigPath string
	LogLevel       string
	Projects       []string
	Rename         bool
	RenameTpl      string
	Split          bool
}

func (c programConfig) String() string {
	return fmt.Sprintf(`{AuthPlugin: '%s', BatchSize: %d, ConfigFile: '%s', DestDir: '%s', KubeconfigPath: '%s', Projects: %v, Rename: %t, RenameTpl: '%s', Split: %t}`,
		c.AuthPlugin, c.BatchSize, c.ConfigFile, c.DestDir, c.KubeconfigPath, c.Projects, c.Rename, c.RenameTpl, c.Split)
}

var cfg programConfig

type credentialsData struct {
	AuthPlugin               string
	CertificateAuthorityData string
	ClusterName              string
	Location                 string
	ProjectID                string
	Server                   string
}

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
		Short:   "Discovers GKE clusters and updates the KUBECONFIG file to include them",
		Long:    "gke-kubeconfiger discovers GKE clusters and updates the KUBECONFIG file to include them.",
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
		String("auth-plugin", "gke-gcloud-auth-plugin", "Name of the auth plugin to use in kubeconfig")

	rootCmd.
		Flags().
		Int("batch-size", 10, "Batch size")

	rootCmd.
		Flags().
		String("dest-dir", ".", "Destination directory to write kubeconfig files.\nIf set, every kubeconfig will be written to a separate file")

	rootCmd.
		Flags().
		String("log-level", "info", "Sets logging level: trace, debug, info, warning, error, fatal, panic")

	rootCmd.
		Flags().
		StringSlice("projects", []string{}, "Projects to filter by")

	rootCmd.
		Flags().
		Bool("rename", false, "Rename kubeconfig contexts")

	rootCmd.
		Flags().
		String("rename-tpl", "{{ .ProjectID }}/{{ .Location }}/{{ .ClusterName }}", "Rename template")

	err := viper.BindPFlags(rootCmd.Flags())
	if err != nil {
		log.WithError(err).Fatal("Couldn't bind flags")
	}

	return rootCmd
}

func run(cmd *cobra.Command, args []string) {
	cfg.ConfigFile = viper.ConfigFileUsed()
	if cfg.ConfigFile == "" {
		log.Debug("No config file used")
	} else {
		log.WithField("config", cfg.ConfigFile).Debug("Using config file")
	}

	var (
		kubeconfig map[string]interface{}
		err        error
	)

	cfg.AuthPlugin = viper.GetString("auth-plugin")
	cfg.BatchSize = viper.GetInt("batch-size")
	cfg.DestDir = viper.GetString("dest-dir")
	cfg.Projects = viper.GetStringSlice("projects")
	cfg.Rename = viper.GetBool("rename")
	cfg.RenameTpl = viper.GetString("rename-tpl")
	cfg.Split = viper.IsSet("dest-dir")

	if !cfg.Rename {
		cfg.RenameTpl = `gke_{{ .ProjectID }}_{{ .Location }}_{{ .ClusterName }}`
	}

	contextNameTemplate, err := template.New("kubeconfig").Parse(cfg.RenameTpl)
	if err != nil {
		log.Fatalf("Failed to parse context name template: %v", err)
	}

	if !cfg.Split {
		if val, ok := os.LookupEnv("KUBECONFIG"); ok {
			cfg.KubeconfigPath = val
		} else if home, errHome := os.UserHomeDir(); errHome == nil {
			cfg.KubeconfigPath = fmt.Sprintf("%s/.kube/config", home)
		} else {
			log.Warnf("Failed to get user home directory: %v", errHome)
			cfg.KubeconfigPath = "kubeconfig.yaml"
		}

		// FIXME: implement file locking
		// The kubeconfig file is read upfront to catch possible errors early,
		// before making any API calls. This file is overwritten later, after
		// all the data is collected. The longer it takes to collect the data,
		// the higher the chance that the kubeconfig file will be modified by
		// another process in the meantime. This can lead to data loss.
		// To prevent this, the kubeconfig file should be locked at the same time
		// as it is read, and the lock should be released only after the file is
		// written back.
		// There is currently no good library for file locking in Go, see the
		// following issue for some options: https://github.com/golang/go/issues/33974
		kubeconfig, err = unmarshalKubeconfigToMap(cfg.KubeconfigPath)
		if err != nil {
			log.Fatalf("Failed to unmarshal kubeconfig: %v", err)
		}
	} else if err = createDirectory(cfg.DestDir); err != nil {
		log.Fatalf("Failed to create directory: %v", err)
	}

	log.WithField("config", cfg).Debug("Configuration")

	projects := make(chan string, cfg.BatchSize)
	filteredProjects := make(chan string, cfg.BatchSize)
	credentials := make(chan credentialsData, cfg.BatchSize)

	if len(cfg.Projects) > 0 {
		for _, project := range cfg.Projects {
			projects <- project
		}
		close(projects)
	} else {
		go getProjects(projects)
	}

	go filterProjects(projects, filteredProjects)
	go getCredentials(filteredProjects, credentials, cfg.AuthPlugin)

	if cfg.Split {
		writeCredentialsToFile(credentials, cfg.DestDir, contextNameTemplate)
	} else {
		inflateKubeconfig(credentials, kubeconfig)
		writeKubeconfigToFile(encodeKubeconfig(kubeconfig), cfg.KubeconfigPath)
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

func getCredentials(in <-chan string, out chan<- credentialsData, authPlugin string) {
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
					out <- credentialsData{
						AuthPlugin:               authPlugin,
						CertificateAuthorityData: cluster.MasterAuth.ClusterCaCertificate,
						ClusterName:              cluster.Name,
						Location:                 cluster.Location,
						ProjectID:                project,
						Server:                   fmt.Sprintf("https://%s", cluster.Endpoint),
					}
					wg.Done()
				}(cluster)
			}
			wg.Done()
		}(project)
	}
	wg.Wait()
	close(out)
}

func writeCredentialsToFile(credentials <-chan credentialsData, destDir string, contextNameTemplate *template.Template) {
	for data := range credentials {
		contextNameBytes := &bytes.Buffer{}
		err := contextNameTemplate.Execute(contextNameBytes, map[string]string{
			"Server":      data.Server,
			"ProjectID":   data.ProjectID,
			"Location":    data.Location,
			"ClusterName": data.ClusterName,
		})
		if err != nil {
			log.Fatalf("Failed to execute kubeconfig template: %v", err)
		}
		filename := fmt.Sprintf("%s_%s_%s.yaml", data.ProjectID, data.Location, data.ClusterName)
		filepath := filepath.Join(destDir, filename)
		kubeconfig := getEmptyKubeconfig()
		addCredentialsToKubeconfig(kubeconfig, data, contextNameBytes.String())
		writeKubeconfigToFile(encodeKubeconfig(kubeconfig), filepath)
	}
}

func inflateKubeconfig(credentials <-chan credentialsData, kubeconfig map[string]interface{}) {
	for data := range credentials {
		clusterName := fmt.Sprintf("gke_%s_%s_%s", data.ProjectID, data.Location, data.ClusterName)
		addCredentialsToKubeconfig(kubeconfig, data, clusterName)
	}
}

func addCredentialsToKubeconfig(kubeconfig map[string]interface{}, data credentialsData, clusterName string) {
	replaceOrAppend(kubeconfig, "clusters", clusterName, "cluster", map[string]interface{}{
		"certificate-authority-data": data.CertificateAuthorityData,
		"server":                     data.Server,
	})
	replaceOrAppend(kubeconfig, "contexts", clusterName, "context", map[string]interface{}{
		"cluster": clusterName,
		"user":    userName,
	})
	replaceOrAppend(kubeconfig, "users", userName, "user", map[string]interface{}{
		"exec": map[string]interface{}{
			"apiVersion":         "client.authentication.k8s.io/v1beta1",
			"command":            data.AuthPlugin,
			"installHint":        "Install gke-gcloud-auth-plugin for use with kubectl by following https://cloud.google.com/kubernetes-engine/docs/how-to/cluster-access-for-kubectl#install_plugin",
			"provideClusterInfo": true,
		},
	})
}

func replaceOrAppend(kubeconfig map[string]interface{}, listName, itemName, key string, value interface{}) {
	list := kubeconfig[listName].([]interface{})
	for _, rawItem := range list {
		item := rawItem.(map[string]interface{})
		if item["name"] == itemName {
			item[key] = value
			return
		}
	}

	list = append(list, map[string]interface{}{
		"name": itemName,
		key:    value,
	})
	kubeconfig[listName] = list
}

func writeKubeconfigToFile(kubeconfig io.Reader, filepath string) {
	out, err := os.OpenFile(filepath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		log.Fatalf("Failed to create file: %v", err)
	}
	defer out.Close()
	_, err = io.Copy(out, kubeconfig)
	if err != nil {
		log.Fatalf("Failed to write file: %v", err)
	}
}

func createDirectory(dir string) error {
	if ok, err := isExist(dir); err != nil {
		return err
	} else if ok {
		return nil
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		log.Errorf("Failed to create directory: %v", err)
		return err
	}
	return nil
}

func isExist(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		log.Warnf("Unable to stat file: %v", err)
		return false, nil
	}
	return false, err
}

func unmarshalKubeconfigToMap(filePath string) (map[string]interface{}, error) {
	ok, err := isExist(filePath)
	if err != nil {
		return nil, err
	}
	if !ok {
		return getEmptyKubeconfig(), nil
	}

	file, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var config map[string]interface{}
	err = yaml.Unmarshal(file, &config)
	if err != nil {
		return nil, err
	}
	return config, nil
}

func getEmptyKubeconfig() map[string]interface{} {
	return map[string]interface{}{
		"apiVersion":      "v1",
		"clusters":        []interface{}{},
		"contexts":        []interface{}{},
		"current-context": "",
		"kind":            "Config",
		"preferences":     map[string]interface{}{},
		"users":           []interface{}{},
	}
}

func encodeKubeconfig(kubeconfig map[string]interface{}) *bytes.Buffer {
	kubeconfigBytes := &bytes.Buffer{}
	enc := yaml.NewEncoder(kubeconfigBytes)
	enc.SetIndent(2)
	if err := enc.Encode(kubeconfig); err != nil {
		log.Fatalf("Failed to encode kubeconfig YAML: %v", err)
	}
	return kubeconfigBytes
}
