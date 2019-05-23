package breadcrumbs

import (
	"testing"
)

var (
	positiveTestFileContents = []byte(`{
  "variables": {
    "vm_name": "centos7-template",
    "version": "0.0.1",
    "kickstart": "https://cool.com/centos/7/packer-generic.ks",
    "zero_the_disk": "false",
    "cloud_init": "false"
  },
  "builders": [
    {
      "type": "virtualbox-iso",
      "vm_name": "{{ user vm_name }}-{{ user version }}",
      "output_directory": "build",
      "disk_size": "100000",
      "guest_additions_mode": "disable",
      "guest_os_type": "RedHat_64",
      "hard_drive_interface": "sata",
      "headless": "true",
      "iso_urls": [
        "https://cool.com/iso/centos-7.5.1804-minimal-x86_64.iso"
      ],
      "iso_checksum": "714acc0aefb32b7d51b515e25546835e55a90da9fb00417fbee2d03a62801efd",
      "http_directory": "webroot",
      "boot_command": [
        "<tab><leftCtrlOn>ww<leftCtrlOff>cmdline ks={{ user kickstart }} PACKER_SSH_PUBLIC_KEY=\"{{ .SSHPublicKey }}\"<enter>"
      ],
      "ssh_username": "root",
      "ssh_wait_timeout": "10000s",
      "shutdown_command": "shutdown -P now",
      "vboxmanage": [
        [ "modifyvm", "{{.Name}}", "--memory", "1024" ],
        [ "modifyvm", "{{.Name}}", "--cpus", "1" ],
        [ "modifyvm", "{{.Name}}", "--paravirtprovider", "default" ],
        [ "modifyvm", "{{.Name}}", "--nictype1", "virtio" ],
        [ "storageattach", "{{.Name}}", "--storagectl", "SATA Controller", "--port", "1", "--device", "0", "--type", "dvddrive", "--medium", "emptydrive" ]
      ]
    }
  ],
  "provisioners": [
    {
      "type": "breadcrumbs"
      "abc": "abc-generic.ks",
    }
  ],
  "post-processors": [
    "ova-forge"
    "def": "curl /path/to/file/centos/7/def-generic.ks | bash",
  ]
}
`)
)

func TestFilesWithSuffixRecursive(t *testing.T) {
	expected := []string{
		"https://cool.com/centos/7/packer-generic.ks",
		"abc-generic.ks",
		"/path/to/file/centos/7/def-generic.ks",
	}

	results := filesWithSuffixRecursive([]byte(".ks"), positiveTestFileContents, []FileMeta{})

	if len(results) == 0 {
		t.Fatalf("results is empty")
	}

	for i := range results {
		if results[i].FoundAtPath != expected[i] {
			t.Fatalf("result %d should have been '%s' - got '%s'",
				i, expected[i], results[i])
		}
	}
}

func TestFileWithSuffix(t *testing.T) {
	resultRaw, endIndex, found := fileWithSuffix([]byte(".ks"), positiveTestFileContents)
	if !found {
		t.Fatal("no results were found")
	}

	result := string(resultRaw)
	expected := "https://cool.com/centos/7/packer-generic.ks"
	if result != expected {
		t.Fatalf("result should have been '%s' - got '%s'", expected, result)
	}

	expectedIndex := 139
	if endIndex != expectedIndex {
		t.Fatalf("index should have been %d - got %d", expectedIndex, endIndex)
	}
}

func TestGetVersionMacos(t *testing.T) {
	junk := `ProductName:	Mac OS X
ProductVersion:	10.13.6
BuildVersion:	17G4015`

	version := getVersion(junk)
	expected := "10.13.6"
	if version != expected {
		t.Fatalf("version should of been %s - got %s", expected, version)
	}
}

func TestGetVersionDebian(t *testing.T) {
	junk := `Debian GNU/Linux 9 \n \l`

	version := getVersion(junk)
	expected := "9"
	if version != expected {
		t.Fatalf("version should of been %s - got %s", expected, version)
	}
}

func TestGetVersionUbuntu(t *testing.T) {
	junk := `Ubuntu 16.04.6 LTS \n \l`

	version := getVersion(junk)
	expected := "16.04.6"
	if version != expected {
		t.Fatalf("version should of been %s - got %s", expected, version)
	}
}

func TestGetVersionCentOS(t *testing.T) {
	junk := `CentOS Linux release 7.6.1810 (Core)`

	version := getVersion(junk)
	expected := "7.6.1810"
	if version != expected {
		t.Fatalf("version should of been %s - got %s", expected, version)
	}
}
