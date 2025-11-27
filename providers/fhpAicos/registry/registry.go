package registry

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/distribution/reference"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/file"
	orasRegistry "oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

type FarEdgeRegistryConfig struct {
	Url                     string
	Username                string
	Password                string
	PlainHTTP               bool
	InsecureSkipTLSVerify   bool
	OverrideDefaultRegistry bool
	OverrideRegistry        bool
}

const (
	DefaultRegistry = "docker.io"
)

// TODO
// [ ] Add support for registry secrets
// [ ] Honor requested pull policy
// [ ] Allow arbitrary names for embserve services
func FetchPackage(ctx context.Context, registry FarEdgeRegistryConfig, localRegistry string, image string, cpuArch string, cpuArchVariant string, operatingSystem string) (string, error) {
	logger := log.G(ctx)

	normalizedReference, err := reference.ParseDockerRef(image)
	if err != nil {
		return "", err
	}

	orasReference, err := orasRegistry.ParseReference(normalizedReference.String())
	if err != nil {
		return "", err
	}

	if registry.OverrideDefaultRegistry && orasReference.Registry == DefaultRegistry || registry.OverrideRegistry {
		orasReference.Registry = registry.Url
	}

	repo, err := newRepository(orasReference.String(), logger, registry)
	if err != nil {
		return "", err
	}

	descriptor, err := repo.Resolve(ctx, orasReference.Reference)
	if err != nil {
		return "", err
	}

	digest := descriptor.Digest.String()
	artifactName := "service.json"
	packageFolder := filepath.Join(localRegistry, orasReference.Registry, orasReference.Repository, digest)
	packageFile := filepath.Join(packageFolder, artifactName)

	//Check if folder exists
	stat, err := os.Stat(packageFile)
	if err == nil && !stat.IsDir() {
		logger.Info("Returning cached image " + orasReference.String() + ". Stored on " + packageFile)
		return packageFile, err
	} else if err == nil {
		os.RemoveAll(packageFile)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	// Create a file store
	fs, err := file.New(packageFolder)
	if err != nil {
		return "", err
	}
	defer fs.Close()

	targetPlatform := ocispec.Platform{
		Architecture: cpuArch,
		Variant:      cpuArchVariant,
		OS:           operatingSystem,
	}
	options := oras.CopyOptions{}
	options.WithTargetPlatform(&targetPlatform)

	// Copy from the remote repository to the file store
	orasReference.Reference = digest
	_, err = oras.Copy(ctx, repo, orasReference.Reference, fs, "", options)
	if err != nil {
		fmt.Println("Failed to get image")
		return "", err
	}

	logger.Info("Fetched image " + orasReference.String() + ". Stored on " + packageFile)
	return packageFile, nil
}

func newRepository(repoReference string, logger log.Logger, registryConfig FarEdgeRegistryConfig) (*remote.Repository, error) {
	repo, err := remote.NewRepository(repoReference)
	if err != nil {
		logger.Error("Failed to create repository")
		return nil, err
	}

	// Set PlainHTTP and/or TLS Certificate verification
	repo.PlainHTTP = registryConfig.PlainHTTP
	httpClient := retry.NewClient()
	if httpClient.Transport.(*retry.Transport).Base == nil {
		httpClient.Transport.(*retry.Transport).Base = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: registryConfig.InsecureSkipTLSVerify,
			},
		}
	}
	registryClient := &auth.Client{
		Client: httpClient,
		Cache:  auth.DefaultCache,
	}
	// Configure registry credentials
	if len(registryConfig.Username) > 0 {
		logger.Debug("Registry Username: " + registryConfig.Username)
		logger.Debug("Registry Password: " + registryConfig.Password)
		registryClient.Credential = auth.StaticCredential(registryConfig.Url, auth.Credential{
			Username: registryConfig.Username,
			Password: registryConfig.Password,
		})
	}
	repo.Client = registryClient
	return repo, nil
}
