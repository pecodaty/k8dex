package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/pecodaty/k8dex/internal/generator"
)

func main() {
	var config generator.Config
	flag.StringVar(&config.KubernetesVersion, "kubernetes-version", "", "Kubernetes release, for example v1.34.0")
	flag.StringVar(&config.Source, "source", "", "OpenAPI v2 source URL (defaults to the official tagged Kubernetes source)")
	flag.StringVar(&config.OutputDir, "output-dir", "internal/generated", "generated metadata directory")
	flag.StringVar(&config.ChangesDir, "changes-dir", "api-changes", "API change report directory")
	flag.Parse()
	if config.KubernetesVersion == "" {
		fmt.Fprintln(os.Stderr, "--kubernetes-version is required")
		os.Exit(2)
	}
	if err := generator.Generate(context.Background(), config); err != nil {
		fmt.Fprintf(os.Stderr, "update APIs: %v\n", err)
		os.Exit(1)
	}
}
