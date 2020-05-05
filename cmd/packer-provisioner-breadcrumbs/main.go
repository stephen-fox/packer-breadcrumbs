package main

import (
	"log"

	"github.com/hashicorp/packer/packer/plugin"
	"github.com/stephen-fox/packer-breadcrumbs"
)

var (
	version string
)

func main() {
	server, err := plugin.Server()
	if err != nil {
		log.Fatal(err.Error())
	}

	err = server.RegisterProvisioner(&breadcrumbs.Provisioner{
		Config: breadcrumbs.PluginConfig{
			PluginVersion: version,
		},
	})
	if err != nil {
		log.Fatal(err.Error())
	}

	server.Serve()
}
