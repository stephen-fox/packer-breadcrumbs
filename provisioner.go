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

	projectDirPath string `mapstructure:"-"`
}

type Manifest struct {
	PluginVersion   string            `json:"plugin_version"`
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

	raw = append(raw, '\n')

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
		results, unresolvedIndexes := filesWithSuffixRecursive([]byte(o.config.IncludeSuffixes[i]), templateRaw, []FileMeta{}, []int{})

		for _, index := range unresolvedIndexes {
			resolution := resolvePackerVariables(results[index].FoundAtPath, o.config.PackerUserVars)
			switch resolution.result {
			case unknownVarType:
				return nil, resolution.err
			case missingVar:
				dir, name, err := trimVariableStringToFile(results[index].FoundAtPath)
				if err != nil {
					return nil, fmt.Errorf("failed to trim packer variable syntax - %s", err.Error())
				}

				filePath, err := findFileInDirRecursive(name, filepath.Join(o.config.projectDirPath, dir))
				if err != nil {
					return nil, fmt.Errorf("failed to lookup packer file found in unresolved variable string - %s", err.Error())
				}

				results[index] = newFileMeta(filePath)
			default:
				results[index] = newFileMeta(resolution.str)
			}
		}

		foundFileMetas = append(foundFileMetas, results...)
	}

	gitRev, err := currentGitRevision(o.config.projectDirPath)
	if err != nil {
		return nil, err
	}

	manifest := &Manifest{
		PluginVersion:   o.Version,
		GitRevision:     gitRev,
		PackerBuildName: o.config.PackerBuildName,
		PackerBuildType: o.config.PackerBuilderType,
		PackerUserVars:  o.config.PackerUserVars,
		PackerTemplate:  hashBytes([]byte(path.Base(o.config.TemplatePath))),
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
	// TODO: Something a little more elegant than this.
	os.Exit(123)
}

func filesWithSuffixRecursive(suffix []byte, raw []byte, metas []FileMeta, unresolvedIndexes []int) ([]FileMeta, []int) {
	resultRaw, endIndex, wasFound := fileWithSuffix(suffix, raw)
	if wasFound && len(resultRaw) != len(suffix) {
		result := string(resultRaw)

		if strings.ContainsAny(result, packerVariableDelims) {
			unresolvedIndexes = append(unresolvedIndexes, len(metas))
			metas = append(metas, newUnresolvedFileMeta(result))
		} else {
			metas = append(metas, newFileMeta(result))
		}
	} else if wasFound && endIndex < len(raw) {
		return filesWithSuffixRecursive(suffix, raw[endIndex:], metas, unresolvedIndexes)
	}

	return metas, unresolvedIndexes
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

	startIndex := bytes.LastIndexByte(raw[:suffixStartIndex], delim)
	if startIndex < 0 || startIndex+1 > endDelimIndex {
		return nil, 0, false
	}

	endPackerVarIndex := bytes.Index(raw[startIndex:endDelimIndex], endPackerVariableBytes)
	if endPackerVarIndex > 0 {
		// TODO: Big assumption about line ending.
		lineStartIndex := bytes.LastIndex(raw[:endDelimIndex], newLineBytes)
		if lineStartIndex < 0 {
			lineStartIndex = 0
		}
		varOpenIndex := bytes.Index(raw[lineStartIndex:endDelimIndex], startPackerVariableBytes)
		if varOpenIndex >= 0 {
			startIndex = lineStartIndex + varOpenIndex
		}
	} else {
		// Increase start index by delim len.
		startIndex++
	}

	return raw[startIndex:endDelimIndex], endDelimIndex, true
}

func findFileInDirRecursive(fileName string, dirPath string) (string, error) {
	var result string

	fn := func(fPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if filepath.Base(fPath) == fileName {
			result, err = filepath.Rel(dirPath, fPath)
			if err != nil {
				return err
			}
		}

		return nil
	}

	err := filepath.Walk(dirPath, fn)
	if err != nil {
		return "", err
	}

	if len(result) == 0 {
		return "", fmt.Errorf("failed to find file '%s' in '%s'", fileName, dirPath)
	}

	return result, nil
}

func newUnresolvedFileMeta(str string) FileMeta {
	return FileMeta{
		FoundAtPath: str,
		unresolved:  true,
	}
}

func newFileMeta(filePath string) FileMeta {
	fm := FileMeta{
		Name:         filepath.Base(filePath),
		FoundAtPath:  filePath,
		StoredAtPath: hashBytes([]byte(filePath)),
	}

	if strings.HasPrefix(filePath, httpFilePrefix) {
		fm.Source = HttpHost
	} else if strings.HasPrefix(filePath, httpsFilePrefix) {
		fm.Source = HttpsHost
	} else {
		fm.Source = LocalStorage
	}

	return fm
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
