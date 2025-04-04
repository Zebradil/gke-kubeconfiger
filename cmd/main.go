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
	AddMetadata    bool
	AuthPlugin     string
	Concurrency    int
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
	return fmt.Sprintf(`{AddMetadata: %t, AuthPlugin: '%s', Concurrency: %d, ConfigFile: '%s', DestDir: '%s', KubeconfigPath: '%s', Projects: %v, Rename: %t, RenameTpl: '%s', Split: %t}`, c.AddMetadata, c.AuthPlugin, c.Concurrency, c.ConfigFile, c.DestDir, c.KubeconfigPath, c.Projects, c.Rename, c.RenameTpl, c.Split)
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
		viper.SetConfigName(".gker")
	}

	if err := viper.ReadInConfig(); err == nil {
		log.Info("Using config file:", viper.ConfigFileUsed())
	}
}

func NewRootCmd(version, commit, date string) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:     "gker",
		Short:   "Discovers GKE clusters and updates the KUBECONFIG file to include them",
		Long:    "gker discovers GKE clusters and updates the KUBECONFIG file to include them.",
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
		StringVar(&cfgFile, "config", "", "config file (default is $HOME/.gker.yaml)")

	rootCmd.
		Flags().
		Bool("add-metadata", false, "[EXPERIMENTAL] Add GKE metadata to clusters in kubeconfig")

	rootCmd.
		Flags().
		String("auth-plugin", "gke-gcloud-auth-plugin", "Name of the auth plugin to use in kubeconfig")

	rootCmd.
		Flags().
		Int("concurrency", 10, "Number of concurrent API requests")

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

	cfg.AddMetadata = viper.GetBool("add-metadata")
	cfg.AuthPlugin = viper.GetString("auth-plugin")
	cfg.Concurrency = viper.GetInt("concurrency")
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

	semaphore := make(chan struct{}, cfg.Concurrency)
	filteredProjects := make(chan string)
	credentials := make(chan credentialsData)

	projects := cfg.Projects
	if len(cfg.Projects) == 0 {
		projects = getProjects()
	}

	go filterProjects(semaphore, projects, filteredProjects)
	go getCredentials(semaphore, filteredProjects, credentials, cfg.AuthPlugin)

	if cfg.Split {
		writeCredentialsToFile(credentials, cfg.DestDir, contextNameTemplate, cfg.AddMetadata)
	} else {
		inflateKubeconfig(credentials, kubeconfig, contextNameTemplate, cfg.AddMetadata)
		writeKubeconfigToFile(encodeKubeconfig(kubeconfig), cfg.KubeconfigPath)
	}
}

func getProjects() []string {
	ctx := context.Background()
	crmService, err := crm.NewService(ctx)
	if err != nil {
		log.Fatalf("Failed to create cloudresourcemanager service: %v", err)
	}
	projects, err := crmService.Projects.List().Filter("lifecycleState:ACTIVE").Do()
	if err != nil {
		log.Fatalf("Failed to list projects: %v", err)
	}
	projectIDs := make([]string, 0, len(projects.Projects))
	for _, project := range projects.Projects {
		projectIDs = append(projectIDs, project.ProjectId)
	}
	return projectIDs
}

func filterProjects(semaphore chan struct{}, projects []string, out chan<- string) {
	ctx := context.Background()
	suService, err := su.NewService(ctx)
	if err != nil {
		log.Fatalf("Failed to create serviceusage service: %v", err)
	}
	suServicesService := su.NewServicesService(suService)
	wg := sync.WaitGroup{}
	for _, project := range projects {
		wg.Add(1)
		semaphore <- struct{}{}
		log.Tracef("Filtering project: %s", project)
		go func(project string) {
			defer wg.Done()
			defer func() { <-semaphore }()
			containerServiceRes, err := suServicesService.Get(fmt.Sprintf("projects/%s/services/container.googleapis.com", project)).Do()
			if err != nil {
				log.WithField("projectID", project).Errorf("Failed to get container service: %v", err)
				return
			}
			if containerServiceRes.State == "ENABLED" {
				out <- project
			}
		}(project)
	}
	wg.Wait()
	close(out)
}

func getCredentials(semaphore chan struct{}, in <-chan string, out chan<- credentialsData, authPlugin string) {
	ctx := context.Background()
	containerService, err := cnt.NewService(ctx)
	if err != nil {
		log.Fatalf("Failed to create container service: %v", err)
	}
	wg := sync.WaitGroup{}
	for project := range in {
		wg.Add(1)
		semaphore <- struct{}{}
		go func(project string) {
			defer wg.Done()
			clusters, err := containerService.Projects.Locations.Clusters.List(fmt.Sprintf("projects/%s/locations/-", project)).Do()
			<-semaphore
			if err != nil {
				log.WithField("projectID", project).Errorf("Failed to list clusters: %v", err)
				return
			}
			for _, cluster := range clusters.Clusters {
				wg.Add(1)
				semaphore <- struct{}{}
				go func(cluster *cnt.Cluster) {
					defer wg.Done()
					defer func() { <-semaphore }()
					log.Infof("Cluster: %s (%s)\n", cluster.Name, cluster.Location)
					log.WithFields(log.Fields{
						"projectID":   project,
						"clusterName": cluster.Name,
						"location":    cluster.Location,
						"endpoint":    cluster.Endpoint,
					}).Debug("Cluster")
					out <- credentialsData{
						AuthPlugin:               authPlugin,
						CertificateAuthorityData: cluster.MasterAuth.ClusterCaCertificate,
						ClusterName:              cluster.Name,
						Location:                 cluster.Location,
						ProjectID:                project,
						Server:                   fmt.Sprintf("https://%s", cluster.Endpoint),
					}
				}(cluster)
			}
		}(project)
	}
	wg.Wait()
	close(out)
}

func writeCredentialsToFile(credentials <-chan credentialsData, destDir string, contextNameTemplate *template.Template, withMetadata bool) {
	for data := range credentials {
		filename := fmt.Sprintf("%s_%s_%s.yaml", data.ProjectID, data.Location, data.ClusterName)
		filepath := filepath.Join(destDir, filename)
		kubeconfig := getEmptyKubeconfig()
		addCredentialsToKubeconfig(kubeconfig, data, contextNameTemplate, withMetadata)
		writeKubeconfigToFile(encodeKubeconfig(kubeconfig), filepath)
	}
}

func inflateKubeconfig(credentials <-chan credentialsData, kubeconfig map[string]interface{}, contextNameTemplate *template.Template, withMetadata bool) {
	for data := range credentials {
		addCredentialsToKubeconfig(kubeconfig, data, contextNameTemplate, withMetadata)
	}
}

func addCredentialsToKubeconfig(kubeconfig map[string]interface{}, data credentialsData, contextNameTemplate *template.Template, withMetadata bool) {
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
	contextName := contextNameBytes.String()
	cluster := map[string]interface{}{
		"certificate-authority-data": data.CertificateAuthorityData,
		"server":                     data.Server,
	}
	if withMetadata {
		addMetadataToCluster(cluster, data)
	}
	replaceOrAppend(kubeconfig, "clusters", contextName, "cluster", cluster)
	replaceOrAppend(kubeconfig, "contexts", contextName, "context", map[string]interface{}{
		"cluster": contextName,
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

func addMetadataToCluster(cluster map[string]interface{}, data credentialsData) {
	cluster["gkeMetadata"] = map[string]interface{}{
		"projectID":   data.ProjectID,
		"location":    data.Location,
		"clusterName": data.ClusterName,
	}
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
	out, err := os.OpenFile(filepath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304
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

	file, err := os.ReadFile(filePath) // #nosec G304
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
