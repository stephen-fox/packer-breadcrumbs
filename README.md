# breadcrumbs packer provisoner plugin
A [packer provisioner](https://packer.io/docs/provisioners/index.html) for
storing build-related files and metadata inside your builds.

The plugin collects metadata from packer itself and git. It will parse your
packer template if you opt to save additional files. It creates a manifest
file in JSON format that describes all of these "breadcrumbs", and sticks it
(and any files it discovers) into the machine being built.

By default, the plugin will store data in `/breadcrumbs`. This includes a
manifest file named `breadcrumbs.json` that describes metadata and any saved
files. Files are saved at the root of the breadcrumbs directory, and are named
by hashing their file path (or URL).

If you would like the plugin to save certain files that are referenced in your
packer template, specify the file suffix(es) using the `include_suffixes`
configuration parameter. For example, if you specify `.sh`, the plugin will
find all instances of files ending in `.sh` in your template, and will attempt
to copy them or download them (if they are http URLs) as breadcrumbs.

## Configuration
Like other packer plugins, the plugin is configured in the packer template file
file using a JSON blob.

#### Default configuration
After installing the plugin, you can specify a default plugin configuration in
your packer template like this:
```json
{
  "provisioners": [
    {
      "type": "breadcrumbs"
    }
  ]
}
```

#### Available variables
The following configuration variables are available:

- `include_suffixes` - *array of string* - A list of file suffixes to find in
the packer config. For example, you can specify .ks and .sh files like this:
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

## Saved breadcrumbs data
Breadcrumbs are build metadata and files that the plugin can save in
your builds.

#### Metadata
The plugin will store the following metadata in the manifest file by default:

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
This could be any of the following:
    - centos
    - debian
    - macos
    - redhat
    - ubuntu
    - windows
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

#### Saved files
By default, the plugin will only copy the packer template file. The plugin
permits you to copy additional files, but you must explicitly specify which
file types should be saved.

## Installation
As of Packer version 1.4.1, you need to do the following:

1. Download the compiled binary for your system from the releases / tags page
2. Create the following directory path in your home directory:
`.packer.d/plugins/`
3. Move the plugin into the directory and make sure it is named
`packer-provisioner-breadcrumbs`
4. Make sure it is set as executable (on *nix systems)

## Known issues
The current logic that finds strings representing files is... let's call it
fragile. One particular circumstance that might cause issues is when a file
string does not begin after a valid scan delimiter (a single quote, double
quote, or a space). For example:
```json
[
    {
      "type": "shell",
      "script": "a dir/junk.sh "
    }
]
``` 

... will result in the plugin attempting to copy `dir/junk.sh`, which will
cause a build failure. To avoid this, make sure your targeted files have a
scan delimited immediately after them.
