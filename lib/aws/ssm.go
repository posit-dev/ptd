package aws

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"time"
)

const (
	SSMStartSSHSessionDocumentName = "AWS-StartSSHSession"
	SSMRunShellDocumentName        = "AWS-RunShellScript"
	SessionManagerPluginDir        = "/usr/local/bin"
)

func SsmSendCommand(ctx context.Context, c *Credentials, region string, instanceId string, command []string) error {
	client := ssm.New(ssm.Options{
		Region:      region,
		Credentials: c.credentialsProvider,
	})

	output, err := client.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{instanceId},
		DocumentName: aws.String(SSMRunShellDocumentName),
		Parameters: map[string][]string{
			"commands": command,
		},
	})
	if err != nil {
		return err
	}
	waiter := ssm.NewCommandExecutedWaiter(client)
	err = waiter.Wait(ctx, &ssm.GetCommandInvocationInput{
		CommandId:  output.Command.CommandId,
		InstanceId: aws.String(instanceId),
	}, time.Minute)

	if err != nil {
		return err
	}

	return nil
}
