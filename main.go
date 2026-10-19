// Omni Web Server is a portable local web server for PHP development: Apache with
// automatic *.local virtual hosts, several PHP versions side by side (each
// project picks its own), toggleable PHP extensions and MySQL/MariaDB,
// controlled from a web dashboard or the command line.
package main

import (
	"os"
	"path/filepath"
)

func main() {
	args := os.Args[1:]
	root := resolveRoot(&args)
	if len(args) == 0 {
		args = []string{"serve"}
	}
	os.Exit(runCLI(root, args))
}

// resolveRoot picks the Omni Web Server folder: --root <dir>, the executable's
// folder when it holds omni.json (an installed copy always uses its own
// folder), $OMNI_WEB_SERVER_HOME, or else the executable's folder.
func resolveRoot(args *[]string) string {
	for i := 0; i < len(*args); i++ {
		if (*args)[i] == "--root" && i+1 < len(*args) {
			root := (*args)[i+1]
			*args = append((*args)[:i], (*args)[i+2:]...)
			return absPath(root)
		}
	}
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		exeDir = filepath.Dir(exe)
	}
	if exeDir != "" && exists(filepath.Join(exeDir, configFile)) {
		return exeDir
	}
	if env := os.Getenv(homeEnv); env != "" {
		return absPath(env)
	}
	if exeDir != "" {
		return exeDir
	}
	wd, _ := os.Getwd()
	return wd
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
