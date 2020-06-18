// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// +build !windows

package flare

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/datadog-agent/cmd/agent/common"
	"github.com/DataDog/datadog-agent/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestCreateSecurityAgentArchive(t *testing.T) {
	assert := assert.New(t)

	common.SetupConfig("./test")
	mockConfig := config.Mock()
	mockConfig.Set("compliance_config.dir", "./test/compliance.d")
	zipFilePath := getArchivePath()
	filePath, err := createSecurityAgentArchive(zipFilePath, true, "./test/logs/agent.log")
	defer os.Remove(zipFilePath)

	assert.Nil(err)
	assert.Equal(zipFilePath, filePath)

	// asserts that it as indeed created a permissions.log file
	z, err := zip.OpenReader(zipFilePath)
	assert.NoError(err, "opening the zip shouldn't pop an error")

	var fileNames []string
	for _, f := range z.File {
		fileNames = append(fileNames, f.Name)
	}

	dir := fileNames[0]
	assert.Contains(fileNames, filepath.Join(dir, "compliance.d/cis-docker.yaml"))
	assert.Contains(fileNames, filepath.Join(dir, "logs/agent.log"))
}
