package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/efs"
)

// EFSMountTarget holds a mount target's ID and the security groups currently
// attached to it.
type EFSMountTarget struct {
	ID             string
	SecurityGroups []string
}

// GetEFSMountTargets returns the mount targets for an EFS file system, with each
// target's current security groups. Mirrors the describe_mount_targets +
// describe_mount_target_security_groups probes inside the Python
// attach_efs_security_group. Read-only; safe to call in the pre-fetch layer.
func GetEFSMountTargets(ctx context.Context, c *Credentials, region, fileSystemID string) ([]EFSMountTarget, error) {
	client := efs.New(efs.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	out, err := client.DescribeMountTargets(ctx, &efs.DescribeMountTargetsInput{
		FileSystemId: aws.String(fileSystemID),
	})
	if err != nil {
		return nil, fmt.Errorf("describe mount targets for EFS %s: %w", fileSystemID, err)
	}

	targets := make([]EFSMountTarget, 0, len(out.MountTargets))
	for _, mt := range out.MountTargets {
		if mt.MountTargetId == nil {
			continue
		}
		sgOut, sgErr := client.DescribeMountTargetSecurityGroups(ctx, &efs.DescribeMountTargetSecurityGroupsInput{
			MountTargetId: mt.MountTargetId,
		})
		if sgErr != nil {
			return nil, fmt.Errorf("describe mount target security groups for %s: %w", *mt.MountTargetId, sgErr)
		}
		targets = append(targets, EFSMountTarget{
			ID:             *mt.MountTargetId,
			SecurityGroups: sgOut.SecurityGroups,
		})
	}
	return targets, nil
}

// AttachSecurityGroupToMountTargets attaches securityGroupID to every mount
// target in targets that does not already have it, mirroring the
// modify_mount_target_security_groups loop in the Python attach_efs_security_group.
// Idempotent: already-attached targets are left unchanged. The caller is expected
// to skip this entirely when mount_targets_managed is false.
func AttachSecurityGroupToMountTargets(ctx context.Context, c *Credentials, region, securityGroupID string, targets []EFSMountTarget) error {
	client := efs.New(efs.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	for _, mt := range targets {
		already := false
		for _, sg := range mt.SecurityGroups {
			if sg == securityGroupID {
				already = true
				break
			}
		}
		if already {
			continue
		}
		updated := append(append([]string{}, mt.SecurityGroups...), securityGroupID)
		_, err := client.ModifyMountTargetSecurityGroups(ctx, &efs.ModifyMountTargetSecurityGroupsInput{
			MountTargetId:  aws.String(mt.ID),
			SecurityGroups: updated,
		})
		if err != nil {
			return fmt.Errorf("attach security group %s to mount target %s: %w", securityGroupID, mt.ID, err)
		}
	}
	return nil
}
