//go:build !linux

package main

import "fmt"

func publishDirectory(stage, output string) error {
	return fmt.Errorf("atomic no-replace directory publication requires Linux")
}
