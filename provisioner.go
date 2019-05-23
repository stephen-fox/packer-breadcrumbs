package breadcrumbs

import (
	"bytes"
	"crypto/sha256"
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

	doubleQuoteChar byte = '"'
	possibleDelims       = "'\" "
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

type FileMeta struct {
	Name         string   `json:"name"`
	FoundAtPath  string   `json:"found_at_path"`
	StoredAtPath string   `json:"stored_at_path"`
	Type         fileType `json:"type"`
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

	projectDirPath string `mapstructure:"-"`
}

// TODO: Template version?
type Manifest struct {
	GitRevision     string            `json:"git_revision"`
	PackerBuildName string            `json:"packer_build_name"`
	PackerBuildType string            `json:"packer_build_type"`
	PackerUserVars  map[string]string `json:"packer_user_variables"`
	OSName          string            `json:"os_name"`
	OSVersion       string            `json:"os_version"`
	IncludeSuffixes []string          `json:"include_suffixes"`
	PackerTemplate  string            `json:"packer_template_path"`
	FoundFiles      []FileMeta        `json:"found_files"`
	pTemplateRaw    []byte            `json:"-"`
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
	// TODO: Interpolate user variables.
	err := config.Decode(&o.config, nil, rawConfigs...)
	if err != nil {
		return err
	}

	if len(strings.TrimSpace(o.config.TemplatePath)) == 0 {
		return fmt.Errorf("failed to get packer template path")
	}

	o.config.projectDirPath = filepath.Dir(o.config.TemplatePath)

	if len(strings.TrimSpace(o.config.UploadDirPath)) == 0 {
		o.config.UploadDirPath = "/"
	}

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

		if len(strings.TrimSpace(o.config.ArtifactsDirPath)) == 0 {
			o.config.ArtifactsDirPath, err = ioutil.TempDir("", "breadcrumbs-")
			if err != nil {
				return err
			}
		}

		err = createBreadcrumbs(o.config.ArtifactsDirPath, manifest, o.config.SaveFileSizeBytes)
		if err != nil {
			return err
		}

		return fmt.Errorf("created breadcrumbs at '%s'", o.config.ArtifactsDirPath)
	}

	return nil
}

func (o *Provisioner) Provision(ui packer.Ui, communicator packer.Communicator) error {
	manifest, err := o.newManifest(communicator)
	if err != nil {
		return err
	}

	// TODO: Should this be done during the 'Prepare()' call?
	if len(strings.TrimSpace(o.config.ArtifactsDirPath)) == 0 {
		temp, err := ioutil.TempDir("", "breadcrumbs-")
		if err != nil {
			return err
		}

		o.config.ArtifactsDirPath = filepath.Join(temp, "breadcrumbs")

		err = os.MkdirAll(o.config.ArtifactsDirPath, 0700)
		if err != nil {
			return err
		}
		defer os.RemoveAll(o.config.ArtifactsDirPath)
	}

	err = createBreadcrumbs(o.config.ArtifactsDirPath, manifest, o.config.SaveFileSizeBytes)
	if err != nil {
		return err
	}

	ui.Say(fmt.Sprintf("Uploading breadcrumbs to '%s'...", o.config.UploadDirPath))

	err = communicator.UploadDir("/", o.config.ArtifactsDirPath, nil)
	if err != nil {
		return err
	}

	ui.Say("Successfully uploaded breadcrumbs")

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

	var foundFileMetas []FileMeta

	for i := range o.config.IncludeSuffixes {
		results := filesWithSuffixRecursive([]byte(o.config.IncludeSuffixes[i]), templateRaw, []FileMeta{})

		foundFileMetas = append(foundFileMetas, results...)
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
		PackerTemplate:  hashString(path.Base(o.config.TemplatePath)),
		IncludeSuffixes: o.config.IncludeSuffixes,
		FoundFiles:      foundFileMetas,
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

func (o *Provisioner) Cancel() {
	os.Exit(123)
}

func filesWithSuffixRecursive(suffix []byte, raw []byte, results []FileMeta) []FileMeta {
	result, endIndex, wasFound := fileWithSuffix(suffix, raw)
	if wasFound {
		if len(result) != len(suffix) {
			results = append(results, newFileMeta(result))
		}

		if len(raw) > 0 && endIndex < len(raw) {
			return filesWithSuffixRecursive(suffix, raw[endIndex:], results)
		}
	}

	return results
}

func fileWithSuffix(suffix []byte, raw []byte) (result []byte, endDelimIndex int, wasFound bool) {
	suffixStartIndex := bytes.Index(raw, suffix)
	if suffixStartIndex < 0 {
		return nil, 0, false
	}

	endDelimIndex = suffixStartIndex + len(suffix)

	delim := doubleQuoteChar
	if len(raw) - 1 >= endDelimIndex && bytes.ContainsAny([]byte{raw[endDelimIndex]}, possibleDelims) {
		delim = raw[endDelimIndex]
	}

	start := bytes.LastIndexByte(raw[:suffixStartIndex], delim)
	if start < 0 || start+1 > endDelimIndex {
		return nil, 0, false
	}

	return raw[start+1: endDelimIndex], endDelimIndex,true
}

func newFileMeta(filePathRaw []byte) FileMeta {
	filePath := string(filePathRaw)

	fm := FileMeta{
		Name:         filepath.Base(filePath),
		FoundAtPath:  filePath,
		StoredAtPath: hashBytes(filePathRaw),
	}

	if strings.HasPrefix(filePath, httpFilePrefix) {
		fm.Type = httpFile
	} else if strings.HasPrefix(filePath, httpsFilePrefix) {
		fm.Type = httpsFile
	} else {
		fm.Type = localFile
	}

	return fm
}

func hashString(s string) string {
	return hashBytes([]byte(s))
}

func hashBytes(s []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(s))
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

	for i := range manifest.FoundFiles {
		destDirPath := manifest.FoundFiles[i].DestinationDirPath(rootDirPath)
		err := os.MkdirAll(destDirPath, 0700)
		if err != nil {
			return err
		}

		destPath := path.Join(destDirPath, manifest.FoundFiles[i].StoredAtPath)

		switch manifest.FoundFiles[i].Type {
		case httpFile, httpsFile:
			p, err := url.Parse(manifest.FoundFiles[i].FoundAtPath)
			if err != nil {
				return err
			}

			err = getHttpFile(p, destPath, maxSaveSizeBytes, 30 * time.Second)
			if err != nil {
				return err
			}
		case localFile:
			err := copyLocalFile(manifest.FoundFiles[i].FoundAtPath, destPath)
			if err != nil {
				return fmt.Errorf("failed to copy local file '%s' to '%s' - %s",
					manifest.FoundFiles[i].FoundAtPath, destPath, err.Error())
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
	dest, err := os.OpenFile(destPath, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0600)
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
