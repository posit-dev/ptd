package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	Namespace  string
}

// CreateConfigMap creates a Kubernetes ConfigMap with the vip.toml configuration
func CreateConfigMap(ctx context.Context, env []string, configName string, config string, namespace string) error {
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
		"-n", namespace,
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
	applyCmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", "-", "-n", namespace)
	applyCmd.Env = env
	applyCmd.Stdin = strings.NewReader(string(output))

	if output, err := applyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply configmap failed: %s", string(output))
	}

	return nil
}

// CreateJob creates a Kubernetes Job for running VIP tests.
// Uses JSON serialization to prevent YAML injection via user-controlled fields.
func CreateJob(ctx context.Context, env []string, opts JobOptions) error {
	args := []string{"--tb=short", "-v"}
	if opts.Categories != "" {
		args = append(args, "-m", opts.Categories)
	}

	activeDeadlineSeconds := int64(600)
	backoffLimit := int32(0)

	job := map[string]interface{}{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata": map[string]interface{}{
			"name":      opts.JobName,
			"namespace": opts.Namespace,
			"labels": map[string]string{
				"app.kubernetes.io/name":       "vip-verify",
				"app.kubernetes.io/managed-by": "ptd",
			},
		},
		"spec": map[string]interface{}{
			"backoffLimit":          backoffLimit,
			"activeDeadlineSeconds": activeDeadlineSeconds,
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"restartPolicy": "Never",
					"containers": []map[string]interface{}{
						{
							"name":  "vip",
							"image": opts.Image,
							"args":  args,
							"volumeMounts": []map[string]interface{}{
								{
									"name":      "config",
									"mountPath": "/app/vip.toml",
									"subPath":   "vip.toml",
								},
							},
							"env": []map[string]interface{}{
								{
									"name": "VIP_TEST_USERNAME",
									"valueFrom": map[string]interface{}{
										"secretKeyRef": map[string]string{
											"name": "vip-test-credentials",
											"key":  "username",
										},
									},
								},
								{
									"name": "VIP_TEST_PASSWORD",
									"valueFrom": map[string]interface{}{
										"secretKeyRef": map[string]string{
											"name": "vip-test-credentials",
											"key":  "password",
										},
									},
								},
							},
						},
					},
					"volumes": []map[string]interface{}{
						{
							"name": "config",
							"configMap": map[string]string{
								"name": opts.ConfigName,
							},
						},
					},
				},
			},
		},
	}

	jobJSON, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal job spec: %w", err)
	}

	cmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", "-", "-n", opts.Namespace)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(string(jobJSON))

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply job failed: %s", string(output))
	}

	return nil
}

// StreamLogs follows the logs of the Job pod
func StreamLogs(ctx context.Context, env []string, jobName string, namespace string) error {
	// Wait for pod to be created (timeout after 30 seconds)
	podName, err := waitForPod(ctx, env, jobName, 30*time.Second, namespace)
	if err != nil {
		return err
	}

	// Stream the pod logs
	cmd := exec.CommandContext(ctx, "kubectl", "logs", "-f", podName, "-n", namespace)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// kubectl logs -f exits with code 1 when the pod has already completed,
			// but also for network errors and other failures. Log a warning so the
			// operator knows logs may be incomplete, then continue to WaitForJob.
			slog.Warn("kubectl logs exited with code 1; log output may be incomplete", "pod", podName)
			return nil
		}
		return fmt.Errorf("failed to stream logs: %w", err)
	}

	return nil
}

// waitForPod waits for a pod associated with the job to be created
func waitForPod(ctx context.Context, env []string, jobName string, timeout time.Duration, namespace string) (string, error) {
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
				"-n", namespace,
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
func WaitForJob(ctx context.Context, env []string, jobName string, namespace string, timeout time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("timeout waiting for job to complete")
		case <-ticker.C:
			cmd := exec.CommandContext(ctx, "kubectl", "get", "job", jobName,
				"-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type==\"Complete\")].status},{.status.conditions[?(@.type==\"Failed\")].status}")
			cmd.Env = env

			output, err := cmd.Output()
			if err != nil {
				continue
			}

			if done, success := parseJobStatus(string(output)); done {
				return success, nil
			}
		}
	}
}

// parseJobStatus parses kubectl jsonpath output for job Complete/Failed conditions.
// The output format is "{Complete.status},{Failed.status}" where each is "True", "False", or empty.
// Using a comma separator prevents ambiguity when one condition is absent (outputs as empty string).
// Returns (done, success): done=true means the job has finished.
func parseJobStatus(output string) (done bool, success bool) {
	parts := strings.SplitN(strings.TrimSpace(output), ",", 2)
	if len(parts) >= 1 && parts[0] == "True" {
		return true, true
	}
	if len(parts) >= 2 && parts[1] == "True" {
		return true, false
	}
	return false, false
}

// Cleanup removes the Job and ConfigMap
func Cleanup(ctx context.Context, env []string, jobName string, configName string, namespace string) error {
	// Delete job
	jobCmd := exec.CommandContext(ctx, "kubectl", "delete", "job", jobName, "-n", namespace, "--ignore-not-found")
	jobCmd.Env = env
	if err := jobCmd.Run(); err != nil {
		return fmt.Errorf("failed to delete job: %w", err)
	}

	// Delete configmap
	cmCmd := exec.CommandContext(ctx, "kubectl", "delete", "configmap", configName, "-n", namespace, "--ignore-not-found")
	cmCmd.Env = env
	if err := cmCmd.Run(); err != nil {
		return fmt.Errorf("failed to delete configmap: %w", err)
	}

	return nil
}
