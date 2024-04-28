package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"os"
	"strings"
	"sync"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	cnt "google.golang.org/api/container/v1"
	su "google.golang.org/api/serviceusage/v1"
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

func main() {
	rename := flag.Bool("rename", false, "Rename kubeconfig contexts")
	renameTpl := flag.String("rename-tpl", "{{ .ProjectID }}/{{ .Location }}/{{ .ClusterName }}", "Rename template")
	batchSize := flag.Int("batch-size", 10, "Batch size")
	flag.Parse()

	contextNameTpl := "{{ $longID }}"
	if *rename {
		contextNameTpl = *renameTpl
	}

	kubeconfigTemplate, err := template.New("kubeconfig").Parse(strings.ReplaceAll(KubeconfigBaseTemplate, "<CONTEXT_NAME>", contextNameTpl))
	if err != nil {
		log.Fatalf("Failed to parse kubeconfig template: %v", err)
	}

	projects := make(chan *crm.Project, *batchSize)
	filteredProjects := make(chan *crm.Project, *batchSize)
	completed := make(chan bool)

	go getProjects(projects)
	go filterProjects(projects, filteredProjects)
	go getCredentials(filteredProjects, kubeconfigTemplate, completed)

	for range completed {
	}
}

func getProjects(out chan<- *crm.Project) {
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
		out <- project
	}
	close(out)
}

func filterProjects(in <-chan *crm.Project, out chan<- *crm.Project) {
	ctx := context.Background()
	suService, err := su.NewService(ctx)
	if err != nil {
		log.Fatalf("Failed to create serviceusage service: %v", err)
	}
	suServicesService := su.NewServicesService(suService)
	wg := sync.WaitGroup{}
	for project := range in {
		wg.Add(1)
		go func(project *crm.Project) {
			fmt.Printf("Project: %s (%s)\n", project.Name, project.ProjectId)
			containerServiceRes, err := suServicesService.Get(fmt.Sprintf("projects/%s/services/container.googleapis.com", project.ProjectId)).Do()
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

func getCredentials(in <-chan *crm.Project, kubeconfigTemplate *template.Template, completed chan<- bool) {
	ctx := context.Background()
	containerService, err := cnt.NewService(ctx)
	if err != nil {
		log.Fatalf("Failed to create container service: %v", err)
	}
	wg := sync.WaitGroup{}
	for project := range in {
		wg.Add(1)
		go func(project *crm.Project) {
			clusters, err := containerService.Projects.Locations.Clusters.List(fmt.Sprintf("projects/%s/locations/-", project.ProjectId)).Do()
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
						"ProjectID":                project.ProjectId,
						"Location":                 cluster.Location,
						"ClusterName":              cluster.Name,
					})
					if err != nil {
						log.Fatalf("Failed to execute kubeconfig template: %v", err)
					}
					filename := fmt.Sprintf("%s_%s_%s.yaml", project.ProjectId, cluster.Location, cluster.Name)
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
