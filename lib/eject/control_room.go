package eject

import (
	"fmt"

	"github.com/posit-dev/ptd/lib/types"
)

type ControlRoomConnection struct {
	Category    string `json:"category"`
	Description string `json:"description"`
	Resource    string `json:"resource"`
	SeverAction string `json:"sever_action"`
}

type ControlRoomDetails struct {
	AccountID   string                  `json:"account_id"`
	ClusterName string                  `json:"cluster_name"`
	Domain      string                  `json:"domain"`
	Region      string                  `json:"region"`
	Connections []ControlRoomConnection `json:"connections"`
}

func CollectControlRoomDetails(config interface{}, targetName string) (*ControlRoomDetails, error) {
	var accountID, clusterName, domain, region string

	switch cfg := config.(type) {
	case types.AWSWorkloadConfig:
		accountID = cfg.ControlRoomAccountID
		clusterName = cfg.ControlRoomClusterName
		domain = cfg.ControlRoomDomain
		region = cfg.ControlRoomRegion
	case types.AzureWorkloadConfig:
		accountID = cfg.ControlRoomAccountID
		clusterName = cfg.ControlRoomClusterName
		domain = cfg.ControlRoomDomain
		region = cfg.ControlRoomRegion
	default:
		return nil, fmt.Errorf("unsupported config type for target %s", targetName)
	}

	details := &ControlRoomDetails{
		AccountID:   accountID,
		ClusterName: clusterName,
		Domain:      domain,
		Region:      region,
	}

	details.Connections = buildConnections(details, targetName)

	return details, nil
}

func buildConnections(details *ControlRoomDetails, targetName string) []ControlRoomConnection {
	var conns []ControlRoomConnection

	if details.AccountID != "" {
		conns = append(conns, ControlRoomConnection{
			Category:    "IAM Trust",
			Description: "Cross-account IAM trust allows the control room to manage this workload",
			Resource:    fmt.Sprintf("AWS account %s", details.AccountID),
			SeverAction: "Remove trust policy entries referencing the control room account",
		})
	}

	if details.Domain != "" {
		mimirEndpoint := fmt.Sprintf("https://mimir.%s/api/v1/push", details.Domain)
		conns = append(conns, ControlRoomConnection{
			Category:    "Observability",
			Description: "Alloy remote_write pushes metrics to the control room Mimir instance",
			Resource:    mimirEndpoint,
			SeverAction: "Remove the prometheus.remote_write \"control_room\" block from Alloy config",
		})

		lokiEndpoint := fmt.Sprintf("https://loki.%s/loki/api/v1/push", details.Domain)
		conns = append(conns, ControlRoomConnection{
			Category:    "Observability",
			Description: "Alloy forwards logs to the control room Loki instance",
			Resource:    lokiEndpoint,
			SeverAction: "Remove the loki.write \"control_room\" block from Alloy config",
		})
	}

	if details.Domain != "" {
		// The mimir password secret lives in the control room's secret store
		secretName := fmt.Sprintf("%s.mimir-auth.posit.team", targetName)
		conns = append(conns, ControlRoomConnection{
			Category:    "Secret Sync",
			Description: "Mimir authentication password is synced to the control room's secret store",
			Resource:    secretName,
			SeverAction: "Delete the mimir password entry from the control room's Secrets Manager",
		})
	}

	if details.ClusterName != "" {
		conns = append(conns, ControlRoomConnection{
			Category:    "Team Operator",
			Description: "Team Operator Helm values reference the control room for configuration",
			Resource:    fmt.Sprintf("Control room cluster: %s", details.ClusterName),
			SeverAction: "Reconfigure Helm values to remove control room references",
		})
	}

	return conns
}
