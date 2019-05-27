package breadcrumbs

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/packer/common"
	"github.com/hashicorp/packer/helper/config"
	"github.com/hashicorp/packer/packer"
)

const (
	defaultPackerTemplateSizeBytes = 100000
	defaultSaveFileSizeBytes       = 100000
	jsonPrefix                     = ""
	jsonIndent                     = "    "
	httpFilePrefix                 = "http://"
	httpsFilePrefix                = "https://"

	doubleQuoteChar byte = '"'
	possibleDelims       = "'\" "
)

type FileSource string

const (
	Unknown      FileSource = ""
	LocalStorage FileSource = "local_storage"
	HttpHost     FileSource = "http_host"
	HttpsHost    FileSource = "https_host"
)

type FileMeta struct {
	Name         string     `json:"name"`
	FoundAtPath  string     `json:"found_at_path"`
	StoredAtPath string     `json:"stored_at_path"`
	Source       FileSource `json:"source"`
	unresolved   bool       `json:"-"`
}

func (o FileMeta) DestinationDirPath(rootDirPath string) string {
	return path.Dir(filepath.Join(rootDirPath, o.StoredAtPath))
}

type PluginConfig struct {
	// The following line embeds the 'common.PackerConfig', which is
	// provided by Packer during the 'Prepare()' call. This allows
	// us to decode the configuration for Packer.
	common.PackerConfig `mapstructure:",squash"`

	// TemplatePath is provided by Packer during the 'Prepare()' call.
	// For whatever reason, it is not included in the
	// 'common.PackerConfig' struct.
	TemplatePath string `mapstructure:"packer_template_path"`

	IncludeSuffixes   []string `mapstructure:"include_suffixes"`
	ArtifactsDirPath  string   `mapstructure:"artifacts_dir_path"`
	UploadDirPath     string   `mapstructure:"upload_dir_path"`
	TemplateSizeBytes int64    `mapstructure:"template_size_bytes"`
	SaveFileSizeBytes int64    `mapstructure:"save_file_size_bytes"`
	DebugConfig       bool     `mapstructure:"debug_config"`
	DebugManifest     bool     `mapstructure:"debug_manifest"`
	DebugBreadcrumbs  bool     `mapstructure:"debug_breadcrumbs"`

	ProjectDirPath string `mapstructure:"-"`
	PluginVersion  string `mapstructure:"-"`
}

type Provisioner struct {
	Config PluginConfig
}

func (o *Provisioner) Prepare(rawConfigs ...interface{}) error {
	// TODO: Interpolate user variables.
	err := config.Decode(&o.Config, nil, rawConfigs...)
	if err != nil {
		return err
	}

	if len(strings.TrimSpace(o.Config.TemplatePath)) == 0 {
		return fmt.Errorf("failed to get packer template path")
	}

	o.Config.ProjectDirPath = filepath.Dir(o.Config.TemplatePath)

	if len(strings.TrimSpace(o.Config.UploadDirPath)) == 0 {
		o.Config.UploadDirPath = "/"
	}

	if o.Config.TemplateSizeBytes == 0 {
		o.Config.TemplateSizeBytes = defaultPackerTemplateSizeBytes
	}

	if o.Config.SaveFileSizeBytes == 0 {
		o.Config.SaveFileSizeBytes = defaultSaveFileSizeBytes
	}

	if o.Config.DebugConfig {
		debugRaw, _ := json.MarshalIndent(o.Config, jsonPrefix, jsonIndent)

		return fmt.Errorf("%s", debugRaw)
	}

	if o.Config.DebugManifest {
		manifest, err := newManifest(&o.Config, OptionalManifestFields{})
		if err != nil {
			return err
		}

		out, err := manifest.ToJson()
		if err != nil {
			return err
		}

		return fmt.Errorf("%s", out)
	}

	if o.Config.DebugBreadcrumbs {
		manifest, err := newManifest(&o.Config, OptionalManifestFields{})
		if err != nil {
			return err
		}

		if len(strings.TrimSpace(o.Config.ArtifactsDirPath)) == 0 {
			o.Config.ArtifactsDirPath, err = ioutil.TempDir("", "breadcrumbs-")
			if err != nil {
				return err
			}
		}

		err = createBreadcrumbs(o.Config.ArtifactsDirPath, manifest, o.Config.SaveFileSizeBytes)
		if err != nil {
			return err
		}

		return fmt.Errorf("created breadcrumbs at '%s'", o.Config.ArtifactsDirPath)
	}

	return nil
}

func (o *Provisioner) Provision(ui packer.Ui, communicator packer.Communicator) error {
	var optionalFields OptionalManifestFields

	switch getOSCategory(communicator) {
	case unix:
		var ok bool
		optionalFields.OSName, optionalFields.OSVersion, ok = isRedHat(communicator)
		if ok {
			break
		}
		optionalFields.OSName, optionalFields.OSVersion, ok = isDebian(communicator)
		if ok {
			break
		}
		optionalFields.OSName, optionalFields.OSVersion, ok = isMacos(communicator)
		if ok {
			break
		}
	case windows:
		optionalFields.OSName = "windows"
		optionalFields.OSVersion = windowsVersion(communicator)
	}

	manifest, err := newManifest(&o.Config, optionalFields)
	if err != nil {
		return err
	}

	if len(strings.TrimSpace(o.Config.ArtifactsDirPath)) == 0 {
		temp, err := ioutil.TempDir("", "breadcrumbs-")
		if err != nil {
			return err
		}

		o.Config.ArtifactsDirPath = filepath.Join(temp, "breadcrumbs")

		err = os.MkdirAll(o.Config.ArtifactsDirPath, 0700)
		if err != nil {
			return err
		}
		defer os.RemoveAll(o.Config.ArtifactsDirPath)
	}

	err = createBreadcrumbs(o.Config.ArtifactsDirPath, manifest, o.Config.SaveFileSizeBytes)
	if err != nil {
		return err
	}

	ui.Say(fmt.Sprintf("Uploading breadcrumbs to '%s'...", o.Config.UploadDirPath))

	err = communicator.UploadDir("/", o.Config.ArtifactsDirPath, nil)
	if err != nil {
		return err
	}

	ui.Say("Successfully uploaded breadcrumbs")

	return nil
}

func (o *Provisioner) Cancel() {
	// TODO: Something a little more elegant than this.
	os.Exit(123)
}

func createBreadcrumbs(rootDirPath string, manifest *Manifest, maxSaveSizeBytes int64) error {
	err := os.MkdirAll(rootDirPath, 0700)
	if err != nil {
		return err
	}

	manifestJson, err := manifest.ToJson()
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(path.Join(rootDirPath, "breadcrumbs.json"), manifestJson, 0600)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(path.Join(rootDirPath, manifest.PackerTemplate), manifest.pTemplateRaw, 0600)
	if err != nil {
		return err
	}

	for i := range manifest.FoundFiles {
		destDirPath := manifest.FoundFiles[i].DestinationDirPath(rootDirPath)
		err := os.MkdirAll(destDirPath, 0700)
		if err != nil {
			return err
		}

		destPath := path.Join(destDirPath, manifest.FoundFiles[i].StoredAtPath)

		switch manifest.FoundFiles[i].Source {
		case HttpHost, HttpsHost:
			p, err := url.Parse(manifest.FoundFiles[i].FoundAtPath)
			if err != nil {
				return err
			}

			err = getHttpFile(p, destPath, 0600, maxSaveSizeBytes, 30 * time.Second)
			if err != nil {
				return err
			}
		case LocalStorage:
			err := copyLocalFile(manifest.FoundFiles[i].FoundAtPath, destPath, 0600, maxSaveSizeBytes)
			if err != nil {
				return fmt.Errorf("failed to copy local file '%s' to '%s' - %s",
					manifest.FoundFiles[i].FoundAtPath, destPath, err.Error())
			}
		default:
			return fmt.Errorf("unknown file source '%s'", manifest.FoundFiles[i].Source)
		}
	}

	return nil
}

func getHttpFile(p *url.URL, destPath string, mode os.FileMode, maxSizeBytes int64, timeout time.Duration) error {
	dest, err := os.OpenFile(destPath, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer dest.Close()

	httpClient := &http.Client{
		Timeout: timeout,
	}

	response, err := httpClient.Get(p.String())
	if err != nil {
		return err
	}

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to GET http file '%s' - got status code %d",
			p.String(), response.StatusCode)
	}

	r := io.LimitReader(response.Body, maxSizeBytes)

	_, err = io.Copy(dest, r)
	switch err {
	case nil:
		break
	case io.EOF:
		return fmt.Errorf("http file '%s' exceeds maximum size of %d byte(s)",
			p.String(), maxSizeBytes)
	default:
		return err
	}

	return nil
}

func copyLocalFile(sourcePath string, destPath string, mode os.FileMode, maxSizeBytes int64) error {
	dest, err := os.OpenFile(destPath, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer dest.Close()

	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()

	sourceLimiter := io.LimitReader(source, maxSizeBytes)

	_, err = io.Copy(dest, sourceLimiter)
	switch err {
	case nil:
		break
	case io.EOF:
		return fmt.Errorf("local file '%s' exceeds maximum size of %d byte(s)",
			sourcePath, maxSizeBytes)
	default:
		return err
	}

	return nil
}
