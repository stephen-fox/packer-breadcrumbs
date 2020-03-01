# packer breadcrumbs
A [packer provisioner](https://packer.io/docs/provisioners/index.html) for
storing build-related files and metadata inside your builds.

The plugin collects metadata from packer itself and git. It will parse your
packer template if you opt to save additional files. It creates a manifest
file in JSON format that describes all of these "breadcrumbs", and sticks it
(and any files it discovers) into the machine being built.

By default, the plugin will store data in `/breadcrumbs`. This includes a
manifest file named `breadcrumbs.json` that describes metadata and any
saved files.

If you would like the plugin to save certain files that are referenced in your
packer template, specify the file suffix(es) in the plugin configuration. For
example, if you specify `.sh`, the plugin will find all instances of files
ending in `.sh` in your template, and will attempt to copy them or download
them (if they are http URLs) as breadcrumbs.

## Configuration
Like other packer plugins, the plugin is configured in the packer template file
using a JSON blob.

#### Default configuration
After installing the plugin, you can specify a default plugin configuration in
your packer template config like this:
```json
{
  "provisioners": [
    {
      "type": "breadcrumbs"
    }
  ]
}
```

#### Common configuration example
Most users will probably be interested in saving at least one or two file
types. This is done using the `include_suffixes` configuration parameter
(described below). The following example demonstrates how to find and save any
`.ks` or `.sh` files referenced in your packer template config:
```json
{
  "provisioners": [
    {
      "type": "breadcrumbs",
      "include_suffixes": [".ks", ".sh"]
    }
  ]
}
```

#### Available variables
The following configuration variables are available:

- `include_suffixes` - *array of string* - A list of file suffixes to find in
the packer config. For example: `[".ks", ".sh"]`
- `artifacts_dir_path` - *string* - The directory to save artifacts to. By
default, this is a temporary directory generated when the plugin runs
- `upload_dir_path` - *string* - The directory to upload the breadcrumbs to.
Defaults to `/breadcrumbs` when not specified
- `template_size_bytes` - *int* - The maximum permitted size of the packer
template in bytes
- `save_file_size_bytes` - *int* - The maximum permitted size of any files that
the plugin will save as breadcrumbs in bytes

#### Debug variables
If you would like to verify the plugin's functionality, you can specify any of
the following debug variables. When specified, a debug variable will cause
the build to fail during the "preparation" phase (unless otherwise noted).

- `debug_config` - *boolean* - Reports the parsed plugin configuration as
a plugin configuration error when set to 'true'
- `debug_manifest` - *boolean* - Reports the serialized manifest as a plugin
configuration error when set to 'true'
- `debug_breadcrumbs` - *boolean* - Saves the breadcrumbs and reports the
breadcrumbs directory as a plugin configuration error when set to 'true'

## Saved breadcrumbs
Breadcrumbs are build metadata and files that the plugin can save inside
your builds.

#### Breadcrumbs manifest
The plugin will store the following metadata in the manifest file as a
JSON blob:

- `plugin_version` - *string* - The version of the breadcrumbs plugin used to
generate the manifest
- `git_revision` - *string* - The current git revision hash
- `packer_build_name` - *string* - The name of the packer build
(e.g., 'virtualbox-iso')
- `packer_build_type` - *string* - The packer build type (e.g.,
'virtualbox-iso')
- `packer_user_variables` - *map key:string value:string* - A map of user
variable names to values provided to packer. For example:
```json
{
    "packer_user_variables": {
        "cloud_init": "false",
        "version": "0.0.1"
    }
}
```
- `os_name` - *string* - The operating system name as determined by the plugin.
This could be any of the following values:
    - `centos`
    - `debian`
    - `macos`
    - `redhat`
    - `ubuntu`
    - `windows`
- `os_version` - *string* - The operating system version as determined by
the plugin
- `packer_template_path` - *string* - The path to the packer template that was
used to build the current image (this is relative to the manifest file)
- `include_suffixes` - *array of string* - A list of file suffixes to include
as originally configured in the packer template. For example:
```json
{
   "include_suffixes": [
       ".ks",
       ".sh"
   ]
}
```
- `found_files` - *array of `FileMeta`* - A list of files and their metadata
found when parsing the packer template. A `FileMeta` is a structure containing
metadata about a file. It consists of the following fields:
    - `name` - *string* - The basename of the file (for example, the name of
    '/path/to/my.sh' would be 'my.sh')
    - `found_at_path` - *string* - The file path (relative to the packer
    template config) or the URL where the file was copied from
    - `stored_at_path` - *string* - The file path where the file is stored at
    relative to the manifest file
    - `source` - *string* - The source type of the file. This can be any of the
    following:
        - `local_storage`
        - `http_host`
        - `https_host`

###### Example breadcrumbs manifest
The following is an example of a breadcrumbs manifest JSON blob:
```json
{
    "git_revision": "5f68622e7557de1cada14585a8ebfc344caac7b9",
    "packer_build_name": "virtualbox-iso",
    "packer_build_type": "virtualbox-iso",
    "packer_user_variables": {
        "version": "0.0.1",
        "vm_name": "centos7-template"
    },
    "os_name": "centos",
    "os_version": "7.5.1804",
    "packer_template_path": "3968485b7af549afbf74a620d104f70bfeca73b09b0d1f1f976996a1534e7515",
    "include_suffixes": [
        ".ks",
        ".sh"
    ],
    "found_files": [
        {
            "name": "packer-generic.ks",
            "found_at_path": "https://cool.com/packer-generic.ks",
            "stored_at_path": "76dde02e89dd273b8100570df8ab7605fa8db7b02a0eabc76952d9ba868955a3",
            "type": "https_host"
        },
        {
            "name": "post-install-cleanup.sh",
            "found_at_path": "https://cool.com/post-install-cleanup.sh",
            "stored_at_path": "7ef70aba188cdafb876886d4db64d3d5ec62b04cd3089a8d00371d246c35362a",
            "type": "https_host"
        }
    ]
}
```

#### Saved files
By default, the plugin will only copy the packer template file. The plugin
permits you to copy additional files, but you must explicitly specify which
file types should be saved. Files are saved at the root of the breadcrumbs
directory and are named by SHA256 hashing their file paths or URLs (if
downloaded via HTTP).

## Installation
As of Packer version 1.4.1, you need to do the following:

1. Download the compiled binary for your system from the releases / tags page
2. Create the following directory path in your home directory:
`.packer.d/plugins/`
3. Move the plugin into the directory and make sure it is named
`packer-provisioner-breadcrumbs`
4. Make sure it is set as executable (on *nix systems)

## Known issues
There are some known issues which will (hopefully) be fixed or improved in
the future.

#### The current file-string-finding logic is... fragile
One particular circumstance that will cause issues is when a file string does
not end with a valid scan delimiter (a single quote, double quote, or a space).
For example:
```json
[
    {
      "type": "shell",
      "inline": ["curl https://cool.com/my.sh|bash"]
    }
]
``` 

... will result in the plugin downloading `curl https://cool.com/my.sh`, which
will cause a build failure. To avoid this, make sure you place a valid scan
delimiter immediately after any file suffixes (in this case, put a space
immediately after `.sh`, or surround the URL with single quotes).

#### The plugin fails to find a file specified in packer variable(s)
The current packer variable resolution logic is pretty basic. At the time of
writing, the logic will attempt to find a file by its basename (e.g.,
`{{ .HTTPIP }}:{{ .HTTPPort}}/ks.ks` would be `ks.ks`). The plugin will search
the directory containing the packer template for the file.

## Building from source
You can use any of the following methods to build the plugin:

- `go build cmd/packer-breadcrumbs/main.go` - Build the plugin directly with
the go CLI
- `build.sh` - A simple wrapper around 'go build' that saves build artifacts
to `build/` and sets a version number in the compiled binary. This script
expects a version to be provided by setting an environment variable
named `VERSION`
- `buildall.sh` - Build the plugin for all supported OSes by wrapping the
`build.sh` script
