# Grafana Dashboard Management

This directory contains JSON definitions for Grafana dashboards deployed with Posit Team Dedicated (PTD).

**Important:** Dashboard provisioning via ConfigMaps is currently only supported for AWS workloads.

Dashboards are deployed as Kubernetes ConfigMaps and automatically loaded into Grafana. The deployment process:

1. JSON files in this directory are read by `pulumi_resources/aws_eks_cluster.py` (method `_create_dashboard_configmaps`)
2. Each JSON file becomes a ConfigMap in the `grafana` namespace
3. Grafana's dashboard provisioning sidecar watches these ConfigMaps and loads dashboards automatically
4. Changes to JSON files trigger ConfigMap updates, which Grafana detects and reloads
5. The dashboard `uid` is automatically set to match a sanitized version of the filename (without `.json` extension) for idempotency

**Important:** The `version` field in dashboard JSON is **not used** for version control since we're deploying via ConfigMap (AWS).