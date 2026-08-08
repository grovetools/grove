package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/pkg/machine"
	"github.com/grovetools/core/pkg/syncproto"
)

func newMachinesApproveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "approve <name-or-id-prefix>",
		Short: "Approve a pending machine after comparing its fingerprint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMachinesApprove(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), args[0], yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Approve without the interactive yes prompt (the fingerprint is still printed)")
	return cmd
}

func newMachinesRevokeCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "revoke <name-or-id-prefix>",
		Short: "Revoke a machine and its descendant device sessions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMachinesRevoke(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), args[0], yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

func newMachinesEnrollCodeCmd() *cobra.Command {
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:   "enroll-code",
		Short: "Mint a short-lived, single-use machine enrollment code",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMachinesEnrollCode(cmd.Context(), cmd.OutOrStdout(), ttl)
		},
	}
	cmd.Flags().DurationVar(&ttl, "ttl", 10*time.Minute, "How long the single-use code remains valid")
	return cmd
}

func listServerDevices(ctx context.Context, client *deviceSessionHTTP) ([]syncproto.DeviceInfo, error) {
	var response syncproto.DeviceListResponse
	if err := client.doJSON(ctx, http.MethodGet, "/sync/devices", nil, &response); err != nil {
		return nil, err
	}
	return response.Devices, nil
}

func resolveServerDevice(devices []syncproto.DeviceInfo, query string, status string) (*syncproto.DeviceInfo, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("machine name or id prefix is required")
	}
	var matches []syncproto.DeviceInfo
	for _, device := range devices {
		if status != "" && device.Status != status {
			continue
		}
		if device.DeviceID == query || strings.HasPrefix(device.DeviceID, strings.ToUpper(query)) || device.Name == query {
			matches = append(matches, device)
		}
	}
	if len(matches) == 0 {
		if status != "" {
			return nil, fmt.Errorf("no %s machine matches %q", status, query)
		}
		return nil, fmt.Errorf("no machine matches %q", query)
	}
	if len(matches) > 1 {
		var labels []string
		for _, device := range matches {
			labels = append(labels, machine.Describe(device.Name, device.DeviceID))
		}
		return nil, fmt.Errorf("machine %q is ambiguous: %s", query, strings.Join(labels, ", "))
	}
	return &matches[0], nil
}

func runMachinesApprove(ctx context.Context, in io.Reader, out io.Writer, query string, yes bool) error {
	client, err := loadDeviceSessionHTTP(ctx)
	if err != nil {
		return err
	}
	return runMachinesApproveWithClient(ctx, in, out, query, yes, client)
}

func runMachinesApproveWithClient(ctx context.Context, in io.Reader, out io.Writer, query string, yes bool, client *deviceSessionHTTP) error {
	devices, err := listServerDevices(ctx, client)
	if err != nil {
		return err
	}
	device, err := resolveServerDevice(devices, query, syncproto.DeviceStatusPending)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Machine:     %s\nFingerprint: %s\n", machine.Describe(device.Name, device.DeviceID), device.Fingerprint)
	if !yes {
		ok, err := confirmFrom(in, out, "Approve this exact fingerprint?")
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("approval aborted")
		}
	}
	var response syncproto.DeviceApprovalResponse
	if err := client.doJSON(ctx, http.MethodPost, devicePath(device.DeviceID, "approve"), syncproto.DeviceApprovalRequest{}, &response); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ approved %s\n  fingerprint %s\n", machine.Describe(response.Device.Name, response.Device.DeviceID), response.Device.Fingerprint)
	return nil
}

func runMachinesRevoke(ctx context.Context, in io.Reader, out io.Writer, query string, yes bool) error {
	client, err := loadDeviceSessionHTTP(ctx)
	if err != nil {
		return err
	}
	devices, err := listServerDevices(ctx, client)
	if err != nil {
		return err
	}
	device, err := resolveServerDevice(devices, query, "")
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Machine:     %s\nFingerprint: %s\n", machine.Describe(device.Name, device.DeviceID), device.Fingerprint)
	if !yes {
		ok, err := confirmFrom(in, out, "Revoke this machine and all descendants?")
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("revocation aborted")
		}
	}
	var response syncproto.DeviceRevokeResponse
	if err := client.doJSON(ctx, http.MethodDelete, "/sync/devices/"+device.DeviceID, nil, &response); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ revoked %s (%d devices, %d sessions)\n", machine.Describe(device.Name, device.DeviceID), response.Devices, response.Sessions)
	return nil
}

func runMachinesEnrollCode(ctx context.Context, out io.Writer, ttl time.Duration) error {
	if ttl < time.Minute || ttl > time.Hour {
		return fmt.Errorf("--ttl must be between 1m and 1h")
	}
	client, err := loadDeviceSessionHTTP(ctx)
	if err != nil {
		return err
	}
	var response syncproto.EnrollCodeResponse
	request := syncproto.EnrollCodeRequest{TTLSeconds: int64(ttl / time.Second)}
	if err := client.doJSON(ctx, http.MethodPost, "/sync/enroll-codes", request, &response); err != nil {
		return err
	}
	if response.Code == "" {
		return fmt.Errorf("sync server returned an empty enrollment code")
	}
	fmt.Fprintf(out, "Enrollment code (shown once): %s\nExpires: %s\n", response.Code, response.ExpiresAt)
	return nil
}

func confirmFrom(in io.Reader, out io.Writer, prompt string) (bool, error) {
	fmt.Fprintf(out, "%s [y/N] ", prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
