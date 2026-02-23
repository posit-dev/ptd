package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// JobOptions contains options for creating a VIP verification Job
type JobOptions struct {
	Image      string
	Categories string
	JobName    string
	ConfigName string
}

// CreateConfigMap creates a Kubernetes ConfigMap with the vip.toml configuration
func CreateConfigMap(ctx context.Context, env []string, configName string, config string) error {
	// Create a temporary file with the config
	tmpfile, err := os.CreateTemp("", "vip-config-*.toml")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.WriteString(config); err != nil {
		tmpfile.Close()
		return fmt.Errorf("failed to write config to temp file: %w", err)
	}
	tmpfile.Close()

	// Create ConfigMap from the file
	cmd := exec.CommandContext(ctx, "kubectl", "create", "configmap", configName,
		"--from-file=vip.toml="+tmpfile.Name(),
		"-n", "posit-team",
		"--dry-run=client",
		"-o", "yaml")
	cmd.Env = env

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("kubectl create configmap failed: %s", string(exitErr.Stderr))
		}
		return fmt.Errorf("kubectl create configmap failed: %w", err)
	}

	// Apply the ConfigMap
	applyCmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", "-", "-n", "posit-team")
	applyCmd.Env = env
	applyCmd.Stdin = strings.NewReader(string(output))

	if output, err := applyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply configmap failed: %s", string(output))
	}

	return nil
}

// CreateJob creates a Kubernetes Job for running VIP tests
func CreateJob(ctx context.Context, env []string, opts JobOptions) error {
	// Build args as a proper slice and marshal to JSON to prevent injection
	argsSlice := []string{"--tb=short", "-v"}
	if opts.Categories != "" {
		argsSlice = append(argsSlice, "-m", opts.Categories)
	}
	argsJSON, err := json.Marshal(argsSlice)
	if err != nil {
		return fmt.Errorf("failed to marshal job args: %w", err)
	}
	args := string(argsJSON)

	jobYAML := fmt.Sprintf(`apiVersion: batch/v1
kind: Job
metadata:
  name: %s
  namespace: posit-team
  labels:
    app.kubernetes.io/name: vip-verify
    app.kubernetes.io/managed-by: ptd
spec:
  backoffLimit: 0
  activeDeadlineSeconds: 600
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: vip
        image: %s
        args: %s
        volumeMounts:
        - name: config
          mountPath: /app/vip.toml
          subPath: vip.toml
        env:
        - name: VIP_TEST_USERNAME
          valueFrom:
            secretKeyRef:
              name: vip-test-credentials
              key: username
        - name: VIP_TEST_PASSWORD
          valueFrom:
            secretKeyRef:
              name: vip-test-credentials
              key: password
      volumes:
      - name: config
        configMap:
          name: %s
`, opts.JobName, opts.Image, args, opts.ConfigName)

	cmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", "-", "-n", "posit-team")
	cmd.Env = env
	cmd.Stdin = strings.NewReader(jobYAML)

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply job failed: %s", string(output))
	}

	return nil
}

// StreamLogs follows the logs of the Job pod
func StreamLogs(ctx context.Context, env []string, jobName string) error {
	// Wait for pod to be created (timeout after 30 seconds)
	podName, err := waitForPod(ctx, env, jobName, 30*time.Second)
	if err != nil {
		return err
	}

	// Stream the pod logs
	cmd := exec.CommandContext(ctx, "kubectl", "logs", "-f", podName, "-n", "posit-team")
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		// Don't return error if the pod has already completed
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Exit code 1 from kubectl logs usually means pod is already done
			if exitErr.ExitCode() == 1 {
				return nil
			}
		}
		return fmt.Errorf("failed to stream logs: %w", err)
	}

	return nil
}

// waitForPod waits for a pod associated with the job to be created
func waitForPod(ctx context.Context, env []string, jobName string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timeout waiting for pod to be created")
		case <-ticker.C:
			cmd := exec.CommandContext(ctx, "kubectl", "get", "pods",
				"-n", "posit-team",
				"-l", "batch.kubernetes.io/job-name="+jobName,
				"-o", "jsonpath={.items[0].metadata.name}")
			cmd.Env = env

			output, err := cmd.Output()
			if err == nil && len(output) > 0 {
				return string(output), nil
			}
		}
	}
}

// WaitForJob waits for the Job to complete and returns success status
func WaitForJob(ctx context.Context, env []string, jobName string) (bool, error) {
	// Wait for job to complete (timeout after 15 minutes)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("timeout waiting for job to complete")
		case <-ticker.C:
			cmd := exec.CommandContext(ctx, "kubectl", "get", "job", jobName,
				"-n", "posit-team",
				"-o", "jsonpath={.status.conditions[?(@.type==\"Complete\")].status} {.status.conditions[?(@.type==\"Failed\")].status}")
			cmd.Env = env

			output, err := cmd.Output()
			if err != nil {
				continue
			}

			status := strings.TrimSpace(string(output))
			parts := strings.Fields(status)

			// Check if job completed successfully
			if len(parts) >= 1 && parts[0] == "True" {
				return true, nil
			}

			// Check if job failed
			if len(parts) >= 2 && parts[1] == "True" {
				return false, nil
			}
		}
	}
}

// Cleanup removes the Job and ConfigMap
func Cleanup(ctx context.Context, env []string, jobName string, configName string) error {
	// Delete job
	jobCmd := exec.CommandContext(ctx, "kubectl", "delete", "job", jobName, "-n", "posit-team", "--ignore-not-found")
	jobCmd.Env = env
	if err := jobCmd.Run(); err != nil {
		return fmt.Errorf("failed to delete job: %w", err)
	}

	// Delete configmap
	cmCmd := exec.CommandContext(ctx, "kubectl", "delete", "configmap", configName, "-n", "posit-team", "--ignore-not-found")
	cmCmd.Env = env
	if err := cmCmd.Run(); err != nil {
		return fmt.Errorf("failed to delete configmap: %w", err)
	}

	return nil
}
