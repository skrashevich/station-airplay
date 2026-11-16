package pcmbundle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func libraryABI(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	header := make([]byte, 20)
	if _, err = io.ReadFull(file, header); err != nil {
		return "", err
	}
	return elfABI(header)
}
func nativeEnvironment() (string, []string, error) {
	override := os.Getenv("STATION_PCM_LIBRARY")
	if _, err := os.Stat("/system/bin/linker64"); !errors.Is(err, os.ErrNotExist) {
		return androidNativeEnvironment("/", runtime.GOARCH, override)
	}
	if _, err := os.Stat("/system/bin/linker"); !errors.Is(err, os.ErrNotExist) {
		return "", nil, errors.New("Android PCM currently requires ARM64 Android with linker64")
	}
	name := "libasound.so.2"
	if override != "" {
		name = override
	}
	var candidates []string
	if strings.ContainsRune(name, os.PathSeparator) {
		candidates = []string{name}
	} else {
		directories := filepath.SplitList(os.Getenv("LD_LIBRARY_PATH"))
		directories = append(directories, "/system/vendor/quasar/libs", "/lib", "/usr/lib",
			"/lib/arm-linux-gnueabihf", "/usr/lib/arm-linux-gnueabihf",
			"/lib/aarch64-linux-gnu", "/usr/lib/aarch64-linux-gnu", "/opt/lib")
		for _, directory := range directories {
			if directory != "" {
				candidates = append(candidates, filepath.Join(directory, name))
			}
		}
	}
	var failure error
	for _, path := range candidates {
		arch, err := libraryABI(path)
		if err != nil {
			if override != "" {
				failure = errors.Join(failure, fmt.Errorf("ALSA library %s: %w", path, err))
			}
			continue
		}
		loader := "/lib/ld-linux-armhf.so.3"
		if arch == "arm64" {
			loader = "/lib/ld-linux-aarch64.so.1"
		}
		loaderABI, err := libraryABI(loader)
		if err != nil || loaderABI != arch {
			failure = errors.Join(failure, fmt.Errorf("ALSA %s needs a compatible %s loader", path, loader))
			continue
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", nil, err
		}
		return arch, childEnvironment(absolute), nil
	}
	return "", nil, fmt.Errorf("no compatible ARM ALSA library and glibc loader found: %w", errors.Join(failure, errors.New("set STATION_PCM_LIBRARY to the native libasound.so.2 path")))
}
func childEnvironment(library string) []string {
	search := filepath.Dir(library)
	if existing := os.Getenv("LD_LIBRARY_PATH"); existing != "" {
		search += string(os.PathListSeparator) + existing
	}
	env := []string{"STATION_PCM_BACKEND=alsa", "STATION_PCM_LIBRARY=" + library, "LD_LIBRARY_PATH=" + search}
	// Keep explicit config overrides, including intentionally empty values. On
	// the Linkplay firmware the copied /tmp/alsa config remains selected by this
	// main config's hooks; no system config or mixer is modified.
	_, pathSet := os.LookupEnv("ALSA_CONFIG_PATH")
	_, directorySet := os.LookupEnv("ALSA_CONFIG_DIR")
	if !pathSet && !directorySet {
		const config = "/system/workdir/lib/alsa/alsa.conf"
		if info, err := os.Stat(config); err == nil && info.Mode().IsRegular() {
			env = append(env, "ALSA_CONFIG_PATH="+config, "ALSA_CONFIG_DIR="+filepath.Dir(config))
		}
	}
	return env
}

func androidNativeEnvironment(root, hostArch, override string) (string, []string, error) {
	if hostArch != "arm64" {
		return "", nil, errors.New("Android PCM currently requires an ARM64 host")
	}
	loader := filepath.Join(root, "system/bin/linker64")
	library := filepath.Join(root, "system/lib64/libOpenSLES.so")
	if override != "" {
		library = override
	}
	libraryArch, err := libraryABI(library)
	if err != nil || libraryArch != "arm64" {
		return "", nil, errors.New("Android PCM needs an ARM64 OpenSL ES system library")
	}
	loaderArch, err := libraryABI(loader)
	if err != nil || loaderArch != "arm64" {
		return "", nil, errors.New("Android PCM needs a compatible linker64")
	}
	absolute, err := filepath.Abs(library)
	if err != nil {
		return "", nil, err
	}
	search := filepath.Dir(absolute)
	if existing := os.Getenv("LD_LIBRARY_PATH"); existing != "" {
		search += string(os.PathListSeparator) + existing
	}
	return "arm64", []string{"STATION_PCM_BACKEND=opensl", "STATION_PCM_LIBRARY=" + absolute, "LD_LIBRARY_PATH=" + search}, nil
}
