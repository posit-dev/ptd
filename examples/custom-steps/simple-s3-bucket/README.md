# Simple S3 Bucket Custom Step

This is a basic example of a custom PTD step that creates an S3 bucket.

## Structure

- `main.go` - The Pulumi program that creates the S3 bucket
- `go.mod` - Go module dependencies for this custom step

## Usage

To use this custom step in a workload:

1. Copy this directory to your workload's customizations directory:
   ```bash
   cp -r examples/custom-steps/simple-s3-bucket infra/__work__/your-workload/customizations/
   ```

2. Create or update the `manifest.yaml` file in the customizations directory:
   ```yaml
   version: 1
   customSteps:
     - name: custom-s3
       description: "Creates a custom S3 bucket for additional data storage"
       path: simple-s3-bucket/
       insertAfter: persistent
       proxyRequired: false
   ```

3. Run the PTD deploy command to apply the custom step:
   ```bash
   ptd ensure your-workload
   ```
