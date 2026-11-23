//go:build !windows

package main

func prepareBundledAssets(string) (componentPaths, error) {
	return componentPaths{}, nil
}
