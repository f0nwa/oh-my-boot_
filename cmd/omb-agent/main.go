package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"syscall"
	"time"

	"omb/internal/agent"
	"omb/internal/boot"
	"omb/internal/ueficausb"
)

func main() {
	socket := flag.String("socket", "/run/omb/agent.sock", "local control socket")
	config := flag.String("config-path", "/etc/omb/dnsmasq.conf", "managed dnsmasq config")
	state := flag.String("state-path", "/var/lib/omb-agent/network.json", "last successful network config")
	recoveryConfig := flag.String("storage-recovery-config", "/etc/omb/storage-recovery.json", "storage recovery settings")
	hosts := flag.String("hosts-path", "/var/lib/omb-dhcp/hosts", "managed dnsmasq DHCP hosts")
	flag.Parse()
	applier, err := agent.NewApplier(*config, *state, *hosts, agent.SystemRunner{})
	if err != nil {
		log.Fatal(err)
	}
	imagesUser, err := user.Lookup("omb-images")
	if err != nil {
		log.Fatal(err)
	}
	smb := &agent.SMBManager{
		CredentialsDir: "/etc/omb/smb",
		UnitDir:        "/etc/systemd/system",
		MountRoot:      "/mnt/omb-smb",
		UploadRoot:     "/mnt/omb-smb-upload",
		ImagesUID:      imagesUser.Uid,
		ImagesGID:      imagesUser.Gid,
		Systemctl:      agent.SMBCommandSystemctl{},
	}
	windows := &agent.WindowsISOManager{
		SourceRoot:  "/mnt/omb-smb",
		LocalRoot:   agent.LocalISORoot,
		ConfigRoot:  "/etc/omb/winiso",
		MountRoot:   "/mnt/omb-iso/profiles",
		AssetRoot:   "/srv/omb/tftp/windows-profiles",
		AnswerRoot:  "/srv/omb/tftp/windows-answers",
		Systemctl:   agent.SMBCommandSystemctl{},
		WIMCommands: agent.ExecDriverCommandRunner{},
	}
	driverManager := &agent.DriverManager{Root: "/var/lib/omb-agent/drivers", ProfileRoot: "/srv/omb/tftp/windows-profiles", WIMRoot: "/srv/omb/tftp/windows-driver-wims", ConfigRoot: "/srv/omb/tftp/windows-driver-configs"}
	applier.WinPE = &agent.WinPEWaitUpdater{
		CustomTemplatePath:  agent.CustomWinPETemplatePath,
		DefaultTemplatePath: agent.DefaultWinPETemplatePath,
		ProfileRoot:         windows.AssetRoot,
		DriverConfigRoot:    driverManager.ConfigRoot,
	}
	ubuntu := &agent.UbuntuISOManager{SourceRoot: "/mnt/omb-smb", LocalRoot: agent.LocalISORoot, AssetRoot: "/srv/omb/tftp", ScriptPath: "/usr/local/libexec/omb-prepare-ubuntu-source", RegistryRoot: boot.DefaultUbuntuRegistryRoot, LegacyConfigPath: "/etc/omb/boot.env", Runner: agent.ExecDriverCommandRunner{}}
	listener, err := agent.ListenUnix(*socket)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	recovery := &agent.StorageRecovery{SMB: smb, Windows: windows, ConfigPath: *recoveryConfig}
	smbSessions := agent.NewSMBWatcher("/var/lib/omb-agent/smb-sessions.json")
	usbBuilder := &ueficausb.Builder{MediaRoot: "/mnt/omb-iso/profiles", CacheRoot: "/var/lib/omb-agent/uefica-usb", Runner: agent.ExecDriverCommandRunner{}, FreeBytes: agent.FreeBytes}
	diskUsage := &agent.DiskUsageCollector{
		Roots: agent.DiskUsageRoots{
			WindowsProfiles:      windows.AssetRoot,
			WindowsDriverWIMs:    driverManager.WIMRoot,
			WindowsDriverConfigs: driverManager.ConfigRoot,
			WindowsAnswers:       windows.AnswerRoot,
			UbuntuSources:        filepath.Join(ubuntu.AssetRoot, "ubuntu-sources"),
			Drivers:              driverManager.Root,
			UEFICAUSB:            usbBuilder.CacheRoot,
		},
	}
	handler := agent.HandlerWithUEFICAUSB(agent.HandlerWithSMBSessions(agent.HandlerWithDHCPLog(agent.HandlerWithRecovery(applier, smb, windows, driverManager, recovery), agent.NewDHCPLog()), smbSessions), usbBuilder)
	handler = agent.HandlerWithDiskUsage(agent.HandlerWithUbuntuISO(handler, ubuntu), diskUsage)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Recover storage mounts and Windows ISO profiles that failed, for example
	// because the NAS became reachable only after this server booted.
	go recovery.Run(ctx)
	// Follow WinPE clients on the install share to show Setup progress.
	go smbSessions.Run(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("Oh My Boot: Agent listening on %s", *socket)
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
