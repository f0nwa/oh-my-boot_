package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"omb/internal/network"
)

func main() {
	socket := flag.String("socket", "/run/omb/agent.sock", "agent Unix socket")
	config := flag.String("config", "", "JSON network config to apply")
	preview := flag.Bool("dry-run", false, "validate and print dnsmasq config without applying")
	flag.Parse()
	if *config == "" {
		fmt.Fprintln(os.Stderr, "-config is required")
		os.Exit(2)
	}
	if *preview {
		if err := dryRun(*config, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx, *socket, *config, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func dryRun(configPath string, output io.Writer) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read network config: %w", err)
	}
	var config network.Config
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse network config: %w", err)
	}
	result, err := network.RenderDnsmasq(config)
	if err != nil {
		return err
	}
	_, err = io.WriteString(output, result)
	return err
}

func run(ctx context.Context, socket, configPath string, output io.Writer) error {
	config, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read network config: %w", err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agent/apply", bytes.NewReader(config))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("contact network agent: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("network agent rejected config (%d): %s", response.StatusCode, bytes.TrimSpace(body))
	}
	_, err = io.Copy(output, response.Body)
	return err
}
