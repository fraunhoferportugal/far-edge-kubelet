package main

import (
	"context"
	"fmt"
	"os"

	"far-edge-kubelet/providers/fhpAicos/registry"

	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	logruslogger "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
)

func main() {
	reg := registry.FarEdgeRegistryConfig{
		Url:                     "localhost:32000",
		Username:                "",
		Password:                "",
		PlainHTTP:               true,
		OverrideDefaultRegistry: false,
		OverrideRegistry:        false,
	}

	log.L = logruslogger.FromLogrus(logrus.NewEntry(logrus.StandardLogger()))
	ctx := log.WithLogger(context.Background(), log.L)
	logrus.SetLevel(logrus.DebugLevel)

	packageFile, err := registry.FetchPackage(ctx, reg, "./references", os.Args[1], "arm", "v7", "zephyr")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Couldn't fetch the package: %s\n", err.Error())
		os.Exit(1)
	}

	fmt.Println("The file is in:", packageFile)
	os.Exit(0)
}
