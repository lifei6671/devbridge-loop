package app

import (
	"path/filepath"
	"strings"
)

type managedCALogDetails struct {
	caCertDirectory string
	caCertFile      string
	caKeyDirectory  string
	caKeyFile       string
}

func buildManagedCALogDetails(caCertFile string, caKeyFile string) managedCALogDetails {
	normalizedCACertFile := strings.TrimSpace(caCertFile)
	normalizedCAKeyFile := strings.TrimSpace(caKeyFile)
	return managedCALogDetails{
		caCertDirectory: managedCAPathDirectory(normalizedCACertFile),
		caCertFile:      normalizedCACertFile,
		caKeyDirectory:  managedCAPathDirectory(normalizedCAKeyFile),
		caKeyFile:       normalizedCAKeyFile,
	}
}

func managedCAPathDirectory(filePath string) string {
	normalizedFilePath := strings.TrimSpace(filePath)
	if normalizedFilePath == "" {
		return ""
	}
	return filepath.Dir(normalizedFilePath)
}
