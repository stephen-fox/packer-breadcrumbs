package breadcrumbs

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

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

type OptionalManifestFields struct {
	OSName    string
	OSVersion string
}

func newManifest(config *PluginConfig, optionalFields OptionalManifestFields) (*Manifest, error) {
	info, err := os.Stat(config.TemplatePath)
	if err != nil {
		return nil, err
	}

	if info.Size() > config.TemplateSizeBytes {
		return nil, fmt.Errorf("packer template file '%s' size exceedes maximum size of %d byte(s)",
			config.TemplatePath, config.TemplateSizeBytes)
	}

	templateRaw, err := ioutil.ReadFile(config.TemplatePath)
	if err != nil {
		return nil, err
	}

	var foundFileMetas []FileMeta

	for i := range config.IncludeSuffixes {
		results, unresolvedIndexes := filesWithSuffixRecursive([]byte(config.IncludeSuffixes[i]), templateRaw, []FileMeta{}, []int{})

		for _, index := range unresolvedIndexes {
			resolution := resolvePackerVariables(results[index].FoundAtPath, config.PackerUserVars)
			switch resolution.result {
			case unknownVarType:
				return nil, resolution.err
			case missingVar:
				dir, name, err := trimVariableStringToFile(results[index].FoundAtPath)
				if err != nil {
					return nil, fmt.Errorf("failed to trim packer variable syntax - %s", err.Error())
				}

				filePath, err := findFileInDirRecursive(name, filepath.Join(config.ProjectDirPath, dir))
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

	gitRev, err := currentGitRevision(config.ProjectDirPath)
	if err != nil {
		return nil, err
	}

	manifest := &Manifest{
		PluginVersion:   config.PluginVersion,
		GitRevision:     gitRev,
		PackerBuildName: config.PackerBuildName,
		PackerBuildType: config.PackerBuilderType,
		PackerUserVars:  config.PackerUserVars,
		PackerTemplate:  hashBytes([]byte(path.Base(config.TemplatePath))),
		IncludeSuffixes: config.IncludeSuffixes,
		OSName:          optionalFields.OSName,
		OSVersion:       optionalFields.OSVersion,
		FoundFiles:      foundFileMetas,
		pTemplateRaw:    templateRaw,
	}

	return manifest, nil
}

func filesWithSuffixRecursive(suffix []byte, raw []byte, metas []FileMeta, unresolvedIndexes []int) ([]FileMeta, []int) {
	resultRaw, endIndex, wasFound := fileWithSuffix(suffix, raw)
	if wasFound {
		if len(resultRaw) != len(suffix) {
			result := string(resultRaw)
			if strings.ContainsAny(result, packerVariableDelims) {
				unresolvedIndexes = append(unresolvedIndexes, len(metas))
				metas = append(metas, newUnresolvedFileMeta(result))
			} else {
				metas = append(metas, newFileMeta(result))
			}
		}

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
