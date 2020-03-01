#!/bin/bash

if [[ -z "${VERSION}" ]]
then
    echo 'the VERSION environment variable must be set'
    exit 1
fi

set -eux

buildDir='build'
mkdir -p "${buildDir}"

filename='packer-provisioner-breadcrumbs'
if [[ ! -z "${GOOS+x}" ]]
then
    filename="${filename}-${GOOS}"
fi
if [[ ! -z "${GOARCH+x}" ]]
then
    filename="${filename}-${GOARCH}"
fi

go build -ldflags "-X main.version=${VERSION}" -o "${buildDir}/${filename}" cmd/packer-breadcrumbs/main.go
