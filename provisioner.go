package breadcrumbs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
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
)

type osCategory string

const (
	unix    osCategory = "unix"
	windows osCategory = "windows"
)

type fileType string

const (
	unknown   fileType = ""
	localFile fileType = "local_file"
	httpFile  fileType = "http_file"
	httpsFile fileType = "https_file"
)

type fileMeta struct {
	OriginalPath    string   `json:"original_path"`
	Name            string   `json:"name"`
	DestinationPath string   `json:"destination_path"`
	Type            fileType `json:"type"`
}

func (o fileMeta) DestinationDirPath(rootDirPath string) string {
	return path.Dir(filepath.Join(rootDirPath, o.DestinationPath))
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
	DirPath           string   `mapstructure:"dir_path"`
	TemplateSizeBytes int64    `mapstructure:"template_size_bytes"`
	SaveFileSizeBytes int64    `mapstructure:"save_file_size_bytes"`
	DebugConfig       bool     `mapstructure:"debug_config"`
	DebugManifest     bool     `mapstructure:"debug_manifest"`
	DebugBreadcrumbs  bool     `mapstructure:"debug_breadcrumbs"`

	projectDirPath string `mapstructure:"-"`
}

// TODO: Template version?
type Manifest struct {
	GitRevision     string                `json:"git_revision"`
	PackerBuildName string                `json:"packer_build_name"`
	PackerBuildType string                `json:"packer_build_type"`
	PackerUserVars  map[string]string     `json:"packer_user_variables"`
	PackerTemplate  string                `json:"packer_template_base64"`
	OSName          string                `json:"os_name"`
	OSVersion       string                `json:"os_version"`
	IncludeSuffixes []string              `json:"include_suffixes"`
	SuffixesToMeta  map[string][]fileMeta `json:"suffixes_to_file_meta"`
	pTemplateRaw    []byte                `json:"-"`
}

func (o *Manifest) ToJson() ([]byte, error) {
	raw, err := json.MarshalIndent(o, jsonPrefix, jsonIndent)
	if err != nil {
		return nil, err
	}

	return raw, nil
}

type Provisioner struct {
	Version string
	config  PluginConfig
}

func (o *Provisioner) Prepare(rawConfigs ...interface{}) error {
	err := config.Decode(&o.config, nil, rawConfigs...)
	if err != nil {
		return err
	}

	if len(strings.TrimSpace(o.config.TemplatePath)) == 0 {
		return fmt.Errorf("failed to get packer template path")
	}

	o.config.projectDirPath = filepath.Dir(o.config.TemplatePath)

	if o.config.TemplateSizeBytes == 0 {
		o.config.TemplateSizeBytes = defaultPackerTemplateSizeBytes
	}

	if o.config.SaveFileSizeBytes == 0 {
		o.config.SaveFileSizeBytes = defaultSaveFileSizeBytes
	}

	if o.config.DebugConfig {
		debugRaw, _ := json.MarshalIndent(o.config, jsonPrefix, jsonIndent)

		return fmt.Errorf("%s", debugRaw)
	}

	if o.config.DebugManifest {
		manifest, err := o.newManifest(nil)
		if err != nil {
			return err
		}

		out, err := manifest.ToJson()
		if err != nil {
			return err
		}

		return fmt.Errorf("%s", out)
	}

	if o.config.DebugBreadcrumbs {
		manifest, err := o.newManifest(nil)
		if err != nil {
			return err
		}

		if len(strings.TrimSpace(o.config.DirPath)) == 0 {
			o.config.DirPath, err = ioutil.TempDir("", "breadcrumbs-")
			if err != nil {
				return err
			}
		}

		err = createBreadcrumbs(o.config.DirPath, manifest, o.config.SaveFileSizeBytes)
		if err != nil {
			return err
		}

		return fmt.Errorf("created breadcrumbs at '%s'", o.config.DirPath)
	}

	return nil
}

func (o *Provisioner) newManifest(communicator packer.Communicator) (*Manifest, error) {
	info, err := os.Stat(o.config.TemplatePath)
	if err != nil {
		return nil, err
	}

	if info.Size() > o.config.TemplateSizeBytes {
		return nil, fmt.Errorf("packer template file '%s' size exceedes maximum size of %d",
			o.config.TemplatePath, o.config.TemplateSizeBytes)
	}

	templateRaw, err := ioutil.ReadFile(o.config.TemplatePath)
	if err != nil {
		return nil, err
	}

	suffixesToMeta := make(map[string][]fileMeta)

	err = filesWithSuffixesInDir(o.config.projectDirPath, o.config.IncludeSuffixes, o.config.SaveFileSizeBytes, suffixesToMeta)
	if err != nil {
		return nil, fmt.Errorf("failed to find files with suffixes in project directory - %s", err.Error())
	}

	for i := range o.config.IncludeSuffixes {
		results := filesWithSuffixRecursive([]byte(o.config.IncludeSuffixes[i]), '"', templateRaw, []fileMeta{})

		suffixesToMeta[o.config.IncludeSuffixes[i]] = append(suffixesToMeta[o.config.IncludeSuffixes[i]], results...)
	}

	gitRev, err := currentGitRevision(o.config.projectDirPath)
	if err != nil {
		return nil, err
	}

	manifest := &Manifest{
		GitRevision:     gitRev,
		PackerBuildName: o.config.PackerBuildName,
		PackerBuildType: o.config.PackerBuilderType,
		PackerUserVars:  o.config.PackerUserVars,
		PackerTemplate:  path.Base(o.config.TemplatePath),
		IncludeSuffixes: o.config.IncludeSuffixes,
		SuffixesToMeta:  suffixesToMeta,
		pTemplateRaw:    templateRaw,
	}

	if communicator != nil {
		switch getOSCategory(communicator) {
		case unix:
			var ok bool
			manifest.OSName, manifest.OSVersion, ok = isRedHat(communicator)
			if ok {
				break
			}
			manifest.OSName, manifest.OSVersion, ok = isDebian(communicator)
			if ok {
				break
			}
			manifest.OSName, manifest.OSVersion, ok = isMacos(communicator)
			if ok {
				break
			}
		case windows:
			manifest.OSName = "windows"
			manifest.OSVersion = windowsVersion(communicator)
		}
	}

	return manifest, nil
}

func (o *Provisioner) Provision(ui packer.Ui, c packer.Communicator) error {
	return nil
}

func (o *Provisioner) Cancel() {
	os.Exit(123)
}

func filesWithSuffixesInDir(dirPath string, suffixes []string, maxSizeBytes int64, suffixesToPaths map[string][]fileMeta) error {
	fn := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		for i := range suffixes {
			if strings.HasSuffix(path, suffixes[i]) {
				if info.Size() > maxSizeBytes {
					return fmt.Errorf("file '%s' exceedes maximum size of %d bytes",
						path, maxSizeBytes)
				}

				p := strings.TrimPrefix(path, dirPath)
				p = strings.TrimPrefix(p, "/")
				suffixesToPaths[suffixes[i]] = append(suffixesToPaths[suffixes[i]], newFileMeta(p))

				break
			}
		}

		return nil
	}

	err := filepath.Walk(dirPath, fn)
	if err != nil {
		return err
	}

	return nil
}

// TODO: Append delim to suffix to avoid badness.
func filesWithSuffixRecursive(suffix []byte, delim byte, raw []byte, results []fileMeta) []fileMeta {
	result, endIndex, wasFound := filesWithSuffix(suffix, delim, raw)
	if wasFound {
		if len(result) != len(suffix) {
			results = append(results, newFileMeta(string(result)))
		}

		if len(raw) > 0 && endIndex < len(raw) {
			return filesWithSuffixRecursive(suffix, delim, raw[endIndex:], results)
		}
	}

	return results
}

func filesWithSuffix(suffix []byte, delim byte, raw []byte) (result []byte, endIndex int, wasFound bool) {
	end := bytes.Index(raw, suffix)
	if end < 0 {
		return nil, 0, false
	}

	start := bytes.LastIndexByte(raw[:end], delim)
	if start < 0 {
		return nil, 0, false
	}

	end = end + len(suffix)

	return raw[start+1:end], end,true
}

func newFileMeta(filePath string) fileMeta {
	fm := fileMeta{
		Name:         filepath.Base(filePath),
		OriginalPath: filePath,
	}

	if strings.HasPrefix(filePath, httpFilePrefix) {
		fm.Type = httpFile
		fm.DestinationPath = path.Base(filePath)
	} else if strings.HasPrefix(filePath, httpsFilePrefix) {
		fm.Type = httpsFile
		fm.DestinationPath = path.Base(filePath)
	} else {
		fm.Type = localFile
		fm.DestinationPath = filePath
	}

	return fm
}

func currentGitRevision(projectDirPath string) (string, error) {
	git := exec.Command("git", "rev-parse", "HEAD")
	git.Dir = projectDirPath

	raw, err := git.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get current git revision - %s - output: '%s'",
			err.Error(), raw)
	}

	return string(bytes.TrimSpace(raw)), nil
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

	for _, metas := range manifest.SuffixesToMeta {
		for i := range metas {
			destDirPath := metas[i].DestinationDirPath(rootDirPath)
			err := os.MkdirAll(destDirPath, 0700)
			if err != nil {
				return err
			}

			destPath := path.Join(destDirPath, metas[i].Name)

			switch metas[i].Type {
			case httpFile, httpsFile:
				p, err := url.Parse(metas[i].OriginalPath)
				if err != nil {
					return err
				}

				err = getHttpFile(p, destPath, maxSaveSizeBytes, 30 * time.Second)
				if err != nil {
					return err
				}
			case localFile:
				err := copyLocalFile(metas[i].OriginalPath, destPath)
				if err != nil {
					return fmt.Errorf("failed to copy local file '%s' to '%s' - %s",
						metas[i].OriginalPath, destPath, err.Error())
				}
			}
		}
	}

	return nil
}

func getHttpFile(p *url.URL, destPath string, maxSizeBytes int64, timeout time.Duration) error {
	dest, err := os.OpenFile(destPath, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0600)
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
	if err != nil && err == io.EOF {
		return fmt.Errorf("http file '%s' exceeds maximum size of %d byte",
			p.String(), maxSizeBytes)
	}

	return nil
}

func copyLocalFile(sourcePath string, destPath string) error {
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}

	dest, err := os.OpenFile(destPath, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, sourceInfo.Mode())
	if err != nil {
		return err
	}
	defer dest.Close()

	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()

	_, err = io.Copy(dest, source)
	if err != nil {
		return err
	}

	return nil
}
