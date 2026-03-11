# Grafana Dashboard Management

This directory contains JSON definitions for Grafana dashboards deployed with Posit Team Dedicated (PTD).

## Dashboard Deployment

**⚠️ Important: Dashboard provisioning via ConfigMaps is currently only supported for AWS workloads. Azure workloads require manual dashboard import through the Grafana UI.**

### AWS Dashboard Deployment

Dashboards are deployed as Kubernetes ConfigMaps and automatically loaded into Grafana. The deployment process:

1. JSON files in this directory are read by `pulumi_resources/aws_eks_cluster.py` (method `_create_dashboard_configmaps`)
2. Each JSON file becomes a ConfigMap in the `grafana` namespace
3. Grafana's dashboard provisioning sidecar watches these ConfigMaps and loads dashboards automatically
4. Changes to JSON files trigger ConfigMap updates, which Grafana detects and reloads
5. The dashboard `uid` is automatically set to match the filename (without `.json` extension) for idempotency

### Azure Dashboard Deployment

Azure deployments do not currently support automatic dashboard provisioning. To use dashboards on Azure:

1. Export the JSON from this directory
2. Access Grafana in your Azure deployment: `ptd proxy <azure-workload-name>`
3. Navigate to **Dashboards** → **Import**
4. Paste the JSON and click **Import**

**Note:** The rest of this documentation assumes AWS deployment unless otherwise noted.

**Important:** The `version` field in dashboard JSON is **not used** for version control since we're deploying via ConfigMap (AWS) or manual import (Azure). Grafana ignores this field during provisioning. Version numbers are informational only.

## Creating a New Dashboard

### 1. Create Through Grafana UI

1. Access Grafana in your PTD deployment:
   ```bash
   ptd proxy <workload-name>
   # Open browser to Grafana URL
   ```

2. Click **"+ Create"** → **"Dashboard"**

3. Add panels, configure queries, set up variables, etc.

4. Click **"Save dashboard"** (disk icon in top-right)

5. Name your dashboard and click **"Save"**

### 2. Export the JSON

After saving your dashboard:

1. Click the **settings gear icon** (⚙️) in the top-right corner

2. Click **"JSON Model"** in the left sidebar

3. Click **"Copy to Clipboard"** or manually select and copy all JSON

4. Create a new file in this directory:
   ```bash
   cd python-pulumi/src/ptd/grafana_dashboards/
   # Paste the JSON into a new file
   vim my-new-dashboard.json
   ```

### 3. Clean Up the JSON

Before committing, clean up Grafana-generated metadata:

```bash
# Remove the internal ID (Grafana generates this)
jq 'del(.id)' my-new-dashboard.json > tmp.json && mv tmp.json my-new-dashboard.json

# Optional: Set version to 1 (informational only)
jq '.version = 1' my-new-dashboard.json > tmp.json && mv tmp.json my-new-dashboard.json

# Format with consistent indentation
jq '.' my-new-dashboard.json > tmp.json && mv tmp.json my-new-dashboard.json
```

**What to remove:**
- `"id"` field at the root level (Grafana auto-generates IDs)
- Any datasource UIDs that are environment-specific (use `"uid": "mimir"` for Prometheus)

**What gets automatically set (AWS only):**
- `"uid"` field - Will be automatically set to match the filename (without `.json` extension) for idempotency

**What to keep:**
- Panel IDs (these are stable and used for referencing)
- Grid positions (`gridPos`)
- All queries and configurations
- Template variables
- Version number (informational, not functional)

## Editing an Existing Dashboard

### 1. Edit Through Grafana UI

1. Open the dashboard in Grafana

2. Click **"Dashboard settings"** (⚙️) in top-right

3. Make your changes (add/remove panels, modify queries, etc.)

4. Click **"Save dashboard"**

### 2. Update the JSON File

1. Export the JSON (see "Export the JSON" above)

2. **Important:** Replace the *entire* JSON file with the new export
   ```bash
   # Copy the JSON from Grafana
   # Paste into the existing file, replacing all content
   vim posit-team-overview.json
   ```

3. Clean up the JSON (see "Clean Up the JSON" above)

4. Verify the changes:
   ```bash
   # Check JSON syntax
   jq '.' posit-team-overview.json > /dev/null

   # See what changed
   git diff posit-team-overview.json
   ```

### 3. Commit and Deploy

```bash
git add posit-team-overview.json
git commit -m "feat(grafana): add new panel to overview dashboard"

# Deploy to test the changes
ptd ensure <workload-name>
```

## Dashboard Best Practices

### Use Template Variables

Always use template variables for cluster and site filtering:

```json
{
  "templating": {
    "list": [
      {
        "name": "cluster_name",
        "query": "label_values(up, cluster)",
        "type": "query"
      },
      {
        "name": "site_name",
        "query": "label_values(up{cluster=~\"$cluster_name\"}, ptd_site)",
        "type": "query"
      }
    ]
  }
}
```

Reference variables in queries:
```promql
metric_name{cluster=~"$cluster_name", ptd_site=~"$site_name"}
```

### Panel Naming Conventions

- Use descriptive, concise titles
- Include time window in title if relevant: "Requests/min (5m)", "CPU usage (1h avg)"
- Avoid redundant prefixes (panel is already in a section/row)

### Query Best Practices

**Avoid double-counting with max/sum:**
```promql
# ✓ Good - use max() for metrics reported identically by all pods
max by (cluster, ptd_site) (ppm_license_days_left{cluster=~"$cluster_name", ptd_site=~"$site_name"})

# ✗ Bad - sum() will multiply by number of pods
sum by (cluster, ptd_site) (ppm_license_days_left{cluster=~"$cluster_name", ptd_site=~"$site_name"})
```

**Avoid `@ end()` in gauges:**
```promql
# ✓ Good - evaluate at current dashboard time
increase(metric[24h]) / (24 * 60)

# ✗ Bad - locks to end of time range, breaks historical playback
increase(metric[24h] @ end()) / (24 * 60)
```

**Use appropriate functions:**
- `increase()` for cumulative counters over a time window (returns total change)
- `rate()` for per-second rates (multiply by 60 for per-minute)
- `irate()` for instant rate (sensitive to scrape intervals)

### Panel Layout Guidelines

- Dashboard width is 24 grid units
- Use rows (`type: "row"`) to organize related panels
- Standard panel heights: 2-4 (stats), 4-6 (gauges), 5-8 (timeseries)
- Align panels on a consistent grid (avoid overlaps)

### Units Configuration

Use appropriate units to prevent auto-conversion:

```json
{
  "fieldConfig": {
    "defaults": {
      "unit": "decgbytes"  // ✓ Shows GB without converting to TB
    }
  }
}
```

Common units:
- `"none"` - Plain number (use with custom suffix)
- `"percent"` - Percentage (0-100)
- `"decbytes"`, `"decgbytes"` - Decimal bytes/GB
- `"s"`, `"ms"` - Time durations (auto-converts, use "none" + suffix to prevent)
- `"dateTimeAsLocal"` - Unix timestamp as local datetime

To prevent auto-conversion:
```json
{
  "unit": "none",
  "custom": {
    "suffix": " days"  // Shows "43 days" instead of "6.14 weeks"
  }
}
```

## Testing Your Changes

### 1. Validate JSON Syntax

```bash
jq '.' my-dashboard.json > /dev/null
```

### 2. Deploy to a Test Environment

```bash
# Deploy the updated dashboard
ptd ensure <test-workload> --dry-run  # Preview changes
ptd ensure <test-workload>

# Access Grafana
ptd proxy <test-workload>
```

### 3. Verify Dashboard Loads

1. Open Grafana in your browser
2. Navigate to your dashboard
3. Check that all panels load without errors
4. Test variable selectors (cluster, site)
5. Verify queries return data
6. Test time range selector

### 4. Check for Common Issues

- **"No data"**: Check if metrics exist in Prometheus (`ptd proxy` → Prometheus UI)
- **"Template variables failed to init"**: Check variable queries are valid
- **Panels overlap**: Review `gridPos` coordinates
- **Missing panels**: Check panel IDs don't conflict with existing IDs

## Dashboard Files in This Directory

| File | Description |
|------|-------------|
| `posit-team-overview.json` | Main operational dashboard with Workbench, Connect, and Package Manager metrics |

## Troubleshooting

### Dashboard doesn't update after deployment

1. Check ConfigMap was created/updated:
   ```bash
   kubectl get configmap -n grafana
   kubectl describe configmap grafana-dashboard-posit-team-overview -n grafana
   ```

2. Check Grafana logs:
   ```bash
   kubectl logs -n grafana deployment/grafana -f
   ```

3. Force Grafana to reload:
   ```bash
   kubectl rollout restart deployment/grafana -n grafana
   ```

### Dashboard variables not populating

- Verify variable queries use correct metric names
- Check if Prometheus has the required metrics:
  ```bash
  # From Prometheus UI
  label_values(up, cluster)
  ```
- Ensure datasource UID is correct (`"uid": "mimir"`)

### Panels showing "N/A" or "No data"

- Verify metric exists in Prometheus
- Check label selectors match your data (cluster, ptd_site, job, etc.)
- Verify time range is appropriate for the data
- Check if aggregation functions are correct (max vs sum)

### JSON validation errors

```bash
# Check syntax
jq '.' dashboard.json

# Common issues:
# - Trailing commas (not allowed in JSON)
# - Missing closing braces/brackets
# - Unescaped quotes in strings
```

## Contributing

When adding or modifying dashboards:

1. **Test thoroughly** in a development environment
2. **Document** any new variables or unusual configurations
3. **Use conventional commits**: `feat(grafana):`, `fix(grafana):`, etc.
4. **Review diffs** carefully - dashboard JSON changes can be large
5. **Avoid reformatting** existing dashboards without functional changes

## Resources

- [Grafana Dashboard JSON Model](https://grafana.com/docs/grafana/latest/dashboards/build-dashboards/view-dashboard-json-model/)
- [Grafana Provisioning](https://grafana.com/docs/grafana/latest/administration/provisioning/)
- [PromQL Basics](https://prometheus.io/docs/prometheus/latest/querying/basics/)
- [Grafana Panel Editor](https://grafana.com/docs/grafana/latest/panels-visualizations/)
