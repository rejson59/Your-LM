package main

// componentPaths are private, versioned files unpacked from the single Windows
// executable. Android and development builds leave them empty.
type componentPaths struct {
	runtimeRoot string
	adbPath     string
	apkPath     string
}
