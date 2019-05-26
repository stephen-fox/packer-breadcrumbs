package breadcrumbs

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	packerVariableDelims = "{}"
	startPackerVariable  = "{{"
	endPackerVariable    = "}}"
)

var (
	newLineBytes             = []byte{'\n'}
	endPackerVariableBytes   = []byte(endPackerVariable)
	startPackerVariableBytes = []byte(startPackerVariable)
)

type packerVariable string

const (
	none    packerVariable = ""
	user    packerVariable = "user"
	special packerVariable = "special"
)

type varRes string

const (
	resolved       varRes = "resolved"
	unknownVarType varRes = "unknown_variable_type"
	missingVar     varRes = "not_exist"
)

type packerResolutionResult struct {
	str    string
	result varRes
	err    error
}

func resolvePackerVariables(str string, existing map[string]string) packerResolutionResult {
	resolvedString := str

	for i := 0; i < len(str); {
		raw, advance, body, wasFound := nextPackerVariable(str[i:])
		if wasFound {
			i = i + advance

			varType, name := packerVariableTypeAndName(body)
			if varType == none {
				return packerResolutionResult{
					result: unknownVarType,
					err:    fmt.Errorf("unknown packer variable type in '%s'", raw),
				}
			}

			v, ok := existing[name]
			if ok {
				resolvedString = strings.Replace(resolvedString, raw, v, -1)
			} else {
				return packerResolutionResult{
					result: missingVar,
					err:    fmt.Errorf("packer variable '%s' does not exist in the provided varaibles", name),
				}
			}
		} else {
			break
		}
	}

	return packerResolutionResult{
		result: resolved,
		str:    resolvedString,
	}
}

func nextPackerVariable(str string) (raw string, advance int, body string, wasFound bool) {
	startIndex := strings.Index(str, startPackerVariable)
	if startIndex < 0 {
		return "", len(str), "", false
	}

	endIndex := strings.Index(str, endPackerVariable)
	if endIndex < 0 {
		return "", len(str), "", false
	}

	raw = str[startIndex:endIndex+len(endPackerVariable)]

	startIndex = startIndex + len(startPackerVariable)

OUTER:
	for i := range str[startIndex:] {
		switch rune(str[i+startIndex]) {
		case ' ':
			startIndex++
			continue
		case '}':
			return "", startIndex, "", false
		default:
			break OUTER
		}
	}

	return raw, endIndex + len(endPackerVariable), strings.TrimSpace(str[startIndex:endIndex]), true
}

func packerVariableTypeAndName(body string) (packerVariable, string) {
	if strings.HasPrefix(body, "user") {
		startIndex := strings.Index(body, "`")
		if startIndex < 0 {
			return none, ""
		}

		startIndex++

		endIndex := strings.LastIndex(body, "`")
		if endIndex < 0 {
			return none, ""
		}

		return user, body[startIndex:endIndex]
	} else if strings.HasPrefix(body, ".") {
		return special, strings.TrimPrefix(body, ".")
	}

	return none, ""
}

func trimVariableStringToFile(str string) (dir string, name string, err error) {
	lastBraceIndex := strings.LastIndex(str, endPackerVariable)
	if lastBraceIndex < 0 {
		return "", "", fmt.Errorf("'%s' does not contain a packer variable", str)
	}

	str = str[lastBraceIndex+len(endPackerVariable):]

	return filepath.Dir(str), filepath.Base(str), nil
}
