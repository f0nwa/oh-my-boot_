package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"syscall"
	"time"

	"pxe-platform/internal/agent"
)

func main() {
	socket := flag.String("socket", "/run/pxe-platform/agent.sock", "local control socket")
	config := flag.String("config-path", "/etc/pxe-platform/dnsmasq.conf", "managed dnsmasq config")
	state := flag.String("state-path", "/var/lib/pxe-platform-agent/network.json", "last successful network config")
	recoveryConfig := flag.String("storage-recovery-config", "/etc/pxe-platform/storage-recovery.json", "storage recovery settings")
	flag.Parse()
	applier, err := agent.NewApplier(*config, *state, agent.SystemRunner{})
	if err != nil {
		log.Fatal(err)
	}
	imagesUser, err := user.Lookup("pxe-images")
	if err != nil {
		log.Fatal(err)
	}
	smb := &agent.SMBManager{
		CredentialsDir: "/etc/pxe-platform/smb",
		UnitDir:        "/etc/systemd/system",
		MountRoot:      "/mnt/pxe-platform-smb",
		UploadRoot:     "/mnt/pxe-platform-smb-upload",
		ImagesUID:      imagesUser.Uid,
		ImagesGID:      imagesUser.Gid,
		Systemctl:      agent.SMBCommandSystemctl{},
	}
	windows := &agent.WindowsISOManager{
		SourceRoot: "/mnt/pxe-platform-smb",
		LocalRoot:  agent.LocalISORoot,
		ConfigRoot: "/etc/pxe-platform/winiso",
		MountRoot:  "/mnt/pxe-platform-iso/profiles",
		AssetRoot:  "/srv/pxe/tftp/windows-profiles",
		AnswerRoot: "/srv/pxe/tftp/windows-answers",
		Systemctl:  agent.SMBCommandSystemctl{},
	}
	driverManager := &agent.DriverManager{Root: "/var/lib/pxe-platform-agent/drivers", ProfileRoot: "/srv/pxe/tftp/windows-profiles", AnswerRoot: "/srv/pxe/tftp/windows-answers", WIMRoot: "/srv/pxe/tftp/windows-driver-wims", ConfigRoot: "/srv/pxe/tftp/windows-driver-configs"}
	listener, err := agent.ListenUnix(*socket)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	recovery := &agent.StorageRecovery{SMB: smb, Windows: windows, ConfigPath: *recoveryConfig}
	smbSessions := agent.NewSMBWatcher("/var/lib/pxe-platform-agent/smb-sessions.json")
	server := &http.Server{Handler: agent.HandlerWithSMBSessions(agent.HandlerWithDHCPLog(agent.HandlerWithRecovery(applier, smb, windows, driverManager, recovery), agent.NewDHCPLog()), smbSessions), ReadHeaderTimeout: 5 * time.Second}
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
	log.Printf("PXE Network Agent listening on %s", *socket)
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
