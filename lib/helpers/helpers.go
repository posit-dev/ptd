package helpers

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/posit-dev/ptd/lib/types"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	yaml "sigs.k8s.io/yaml/goyaml.v3"
)

// TODO: Much of this code is duplicated from `cmd/internal/legacy`, which
// should ultimately be deprecated in favor of `lib`

const (
	CtrlDir = "__ctrl__"
	WorkDir = "__work__"
)

func GitTop() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

type BaseConfig struct {
	ApiVersion string    `yaml:"apiVersion"`
	Kind       string    `yaml:"kind"`
	Spec       yaml.Node `yaml:"spec"`
}

func LoadPtdYaml(filename string) (interface{}, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var base BaseConfig
	if err := yaml.Unmarshal(data, &base); err != nil {
		return nil, err
	}

	switch base.Kind {
	case "AWSWorkloadConfig":
		var config types.AWSWorkloadConfig
		if err := base.Spec.Decode(&config); err != nil {
			return nil, err
		}
		return config, nil
	case "AzureWorkloadConfig":
		var config types.AzureWorkloadConfig
		if err := base.Spec.Decode(&config); err != nil {
			return nil, err
		}
		return config, nil
	case "AWSControlRoomConfig":
		var config types.AWSControlRoomConfig
		if err := base.Spec.Decode(&config); err != nil {
			return nil, err
		}
		return config, nil
	}

	return nil, fmt.Errorf("unknown kind: %s", base.Kind)
}

func Base64Decode(s string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func AskForConfirmation(s string) bool {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Printf("%s [y/n]: ", s)

		response, err := reader.ReadString('\n')
		if err != nil {
			return false
		}

		response = strings.ToLower(strings.TrimSpace(response))

		if response == "y" || response == "yes" {
			return true
		} else if response == "n" || response == "no" {
			return false
		}
	}
}

func TitleCase(s string) string {
	c := cases.Title(language.English)
	return c.String(s)
}

func GenerateTemporarySSHKey(ctx context.Context) (public string, private string, err error) {
	keyPathDir, err := os.MkdirTemp("", "ssh")
	if err != nil {
		return
	}

	private = filepath.Join(keyPathDir, fmt.Sprintf("ed25519-%d", time.Now().UnixMilli()))
	public = private + ".pub"

	cmd := exec.CommandContext(ctx, "ssh-keygen", "-t", "ed25519", "-N", "", "-f", private, "-C", "ssh-over-ssm")
	err = cmd.Run()
	if err != nil {
		slog.Error("Error running command", "error", err)
		return
	}
	return
}

func GenerateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	charsetLength := big.NewInt(int64(len(charset)))
	result := make([]byte, length)

	for i := 0; i < length; i++ {
		// it appears sufficient to ignore the error here
		// rand.Int returns an error only if max is less than 0 (it can't be)
		// underneath rand.Read _can_ return an error, but the implementation
		// explicitly suggests to ignore it as it will not (ha).
		randomIndex, _ := rand.Int(rand.Reader, charsetLength)
		result[i] = charset[randomIndex.Int64()]
	}
	return string(result)
}

func Sha256Hash(s string, limit int) string {
	// Use the built-in crypto package to hash the string
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(s)))
	if limit > 0 && limit < len(hash) {
		return hash[:limit]
	}
	return hash
}

func YamlPathForTarget(target types.Target) string {
	var subdir string

	switch target.Type() {
	case types.TargetTypeControlRoom:
		subdir = CtrlDir
	case types.TargetTypeWorkload:
		subdir = WorkDir
	default:
		return ""
	}

	return filepath.Join(GetTargetsConfigPath(), subdir, target.Name(), "ptd.yaml")
}

func ConfigForTarget(target types.Target) (interface{}, error) {
	yamlPath := YamlPathForTarget(target)
	if yamlPath == "" {
		return nil, fmt.Errorf("missing ptd.yaml path for target: %s", target.Name())
	}

	conf, err := LoadPtdYaml(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("could not load ptd.yaml file: %w", err)
	}

	return conf, nil
}
