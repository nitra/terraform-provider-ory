package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/nitra/terraform-provider-ory/internal/provider"
)

// Set by goreleaser via -ldflags.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.opentofu.org/nitra/ory",
		Debug:   debug,
	})
	if err != nil {
		log.Fatalf("provider %s (%s): %s", version, commit, err)
	}
}
