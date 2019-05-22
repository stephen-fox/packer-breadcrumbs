package breadcrumbs

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/hashicorp/packer/common"
	"github.com/hashicorp/packer/helper/config"
	"github.com/hashicorp/packer/packer"
)

const (
	defaultPackerTemplateSizeBytes = 100000
	defaultSaveFileSizeBytes       = 100000
	jsonPrefix                     = "    "
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
	onDisk    fileType = "file_on_disk"
	httpFile  fileType = "http_file"
	httpsFile fileType = "https_file"
)

type fileMeta struct {
	Type            fileType `json:"type"`
	OriginalPath    string   `json:"original_path"`
	DestinationPath string   `json:"destination_path"`
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

	projectDirPath string `mapstructure:"-"`

	DebugConfig       bool     `mapstructure:"debug_config"`
	DebugManifest     bool     `mapstructure:"debug_manifest"`
	TemplateSizeBytes int64    `mapstructure:"template_size_bytes"`
	SaveFileSizeBytes int64    `mapstructure:"save_file_size_bytes"`
	IncludeSuffixes   []string `mapstructure:"include_suffixes"`
}

// TODO: Template version?
type Manifest struct {
	SourceControlRev  string                `json:"scm_revision"`
	PackerBuildName   string                `json:"packer_build_name"`
	PackerBuildType   string                `json:"packer_build_type"`
	PackerUserVars    map[string]string     `json:"packer_user_variables"`
	PackerTemplateB64 string                `json:"packer_template_base64"`
	OSName            string                `json:"os_name"`
	OSVersion         string                `json:"os_version"`
	IncludeSuffixes   []string              `json:"include_suffixes"`
	SuffixesToMeta    map[string][]fileMeta `json:"suffixes_to_file_meta"`
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

		debugRaw, _ := json.MarshalIndent(manifest, jsonPrefix, jsonIndent)

		return fmt.Errorf("%s", debugRaw)
	}

	return nil
}

func (o *Provisioner) newManifest(c packer.Communicator) (*Manifest, error) {
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

	err = filesWithSuffixesInRawData(templateRaw, o.config.IncludeSuffixes, suffixesToMeta)
	if err != nil {
		return nil, fmt.Errorf("failed to find files with suffixes in Packer template file - %s", err.Error())
	}

	rev, err := currentGitRevision(o.config.projectDirPath)
	if err != nil {
		return nil, err
	}

	manifest := &Manifest{
		SourceControlRev:  rev,
		PackerBuildName:   o.config.PackerBuildName,
		PackerBuildType:   o.config.PackerBuilderType,
		PackerUserVars:    o.config.PackerUserVars,
		PackerTemplateB64: base64.StdEncoding.EncodeToString(templateRaw),
		IncludeSuffixes:   o.config.IncludeSuffixes,
		SuffixesToMeta:    suffixesToMeta,
	}

	if c != nil {
		switch getOSCategory(c) {
		case unix:
			var ok bool
			manifest.OSName, manifest.OSVersion, ok = isRedHat(c)
			if ok {
				break
			}
			manifest.OSName, manifest.OSVersion, ok = isDebian(c)
			if ok {
				break
			}
			manifest.OSName, manifest.OSVersion, ok = isMacos(c)
			if ok {
				break
			}
		case windows:
			manifest.OSName = "windows"
			manifest.OSVersion = windowsVersion(c)
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

func filesWithSuffixesInRawData(raw []byte, suffixes []string, suffixesToFilePaths map[string][]fileMeta) error {
	for i := range suffixes {
		results := filesInRawData(raw, []byte(suffixes[i]), []fileMeta{})

		suffixesToFilePaths[suffixes[i]] = append(suffixesToFilePaths[suffixes[i]], results...)
	}

	return nil
}

func filesInRawData(raw []byte, suffix []byte, results []fileMeta) []fileMeta {
	result, endIndex, wasFound := suffixesInData(raw, suffix)
	if wasFound {
		if len(result) != len(suffix) {
			results = append(results, newFileMeta(string(result)))
		}

		if len(raw) > 0 && endIndex < len(raw) {
			return filesInRawData(raw[endIndex:], suffix, results)
		}
	}

	return results
}

func newFileMeta(filePath string) fileMeta {
	fm := fileMeta{
		OriginalPath: filePath,
	}

	if strings.HasPrefix(filePath, httpFilePrefix) {
		fm.Type = httpFile
		fm.DestinationPath = path.Base(filePath)
	} else if strings.HasPrefix(filePath, httpsFilePrefix) {
		fm.Type = httpsFile
		fm.DestinationPath = path.Base(filePath)
	} else {
		fm.Type = onDisk
		fm.DestinationPath = filePath
	}

	return fm
}

func suffixesInData(raw []byte, suffix []byte) (result []byte, endIndex int, wasFound bool) {
	end := bytes.Index(raw, suffix)
	if end < 0 {
		return nil, 0, false
	}

	start := bytes.LastIndexByte(raw[:end], '"')
	if start < 0 {
		return nil, 0, false
	}

	end = end + len(suffix)

	return raw[start+1:end], end,true
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
