package breadcrumbs

import (
	"bytes"
	"context"
	"strings"
	"unicode"

	"github.com/hashicorp/packer/packer"
)

type osCategory string

const (
	unix    osCategory = "unix"
	windows osCategory = "windows"
)

func getOSCategory(c packer.Communicator) osCategory {
	ls := &packer.RemoteCmd{
		Command: "ls",
	}

	err := c.Start(context.TODO(), ls)
	if err != nil {
		return osCategory("unknown")
	}

	ls.Wait()

	if ls.ExitStatus() == 0 {
		return unix
	}

	return windows
}

func isRedHat(c packer.Communicator) (string, string, bool) {
	stdout := bytes.NewBuffer(nil)
	cat := &packer.RemoteCmd{
		Command: "cat /etc/redhat-release",
		Stdout:  stdout,
	}

	err := c.Start(context.TODO(), cat)
	if err != nil {
		return "", "", false
	}

	cat.Wait()

	if cat.ExitStatus() != 0 {
		return "", "", false
	}

	name := "redhat"
	outStr := stdout.String()
	if strings.Contains(strings.ToLower(outStr), "centos") {
		name = "centos"
	}

	return name, getVersion(outStr), true
}

func isDebian(c packer.Communicator) (string, string, bool) {
	stdout := bytes.NewBuffer(nil)
	cat := &packer.RemoteCmd{
		Command: "cat /etc/issue",
		Stdout:  stdout,
	}

	err := c.Start(context.TODO(), cat)
	if err != nil {
		return "", "", false
	}

	cat.Wait()

	if cat.ExitStatus() != 0 {
		return "", "", false
	}

	name := "debian"
	outStr := stdout.String()
	if strings.Contains(strings.ToLower(outStr), "ubuntu") {
		name = "ubuntu"
	}

	return name, getVersion(outStr), true
}

func isMacos(c packer.Communicator) (string, string, bool) {
	stdout := bytes.NewBuffer(nil)
	swVers := &packer.RemoteCmd{
		Command: "sw_vers",
		Stdout:  stdout,
	}

	err := c.Start(context.TODO(), swVers)
	if err != nil {
		return "", "", false
	}

	swVers.Wait()

	if swVers.ExitStatus() != 0 {
		return "", "", false
	}

	return "macos", getVersion(stdout.String()), true
}

func windowsVersion(c packer.Communicator) string {
	stdout := bytes.NewBuffer(nil)
	ver := &packer.RemoteCmd{
		Command: "ver",
		Stdout:  stdout,
	}

	err := c.Start(context.TODO(), ver)
	if err != nil {
		return ""
	}

	ver.Wait()

	return getVersion(stdout.String())
}

func getVersion(s string) string {
	version := bytes.NewBuffer(nil)
	save := false
	for i := range s {
		if unicode.IsDigit(rune(s[i])) {
			save = true
		}

		if !save {
			continue
		}

		if !unicode.IsDigit(rune(s[i])) && s[i] != '.' {
			break
		}

		version.WriteByte(s[i])
	}

	return version.String()
}
