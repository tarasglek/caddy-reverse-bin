package main

type Layout struct {
	BinaryPath string
	ConfigPath string
	AppRoot    string
	HomeDir    string
	RuntimeDir string
	LibexecDir string
}

func DebianLayout() Layout {
	return Layout{
		BinaryPath: "/usr/bin/reverse-bin-caddy",
		ConfigPath: "/etc/reverse-bin/Caddyfile",
		AppRoot:    "/var/lib/reverse-bin/apps",
		HomeDir:    "/var/lib/reverse-bin/home",
		RuntimeDir: "/run/reverse-bin",
		LibexecDir: "/usr/lib/reverse-bin",
	}
}
