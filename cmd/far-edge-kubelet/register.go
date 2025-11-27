package main

import (
	"far-edge-kubelet/cmd/far-edge-kubelet/internal/provider"
	fhpAicos "far-edge-kubelet/providers/fhpAicos"
)

func registerFhPAICOS(s *provider.Store) {
	/* #nosec */
	s.Register("fhpAicos", func(cfg provider.InitConfig) (provider.Provider, error) { //nolint:errcheck
		return fhpAicos.NewFhPAICOSProvider(
			cfg.ConfigPath,
			cfg.NodeName,
			cfg.OperatingSystem,
			cfg.InternalIP,
			cfg.DaemonPort,
		)
	})
}
