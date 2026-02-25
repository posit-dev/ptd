#!/usr/bin/env python3
"""
Validate Go struct YAML tags match Python dataclass field names.

USAGE: python scripts/validate-config-sync.py

Compares YAML field names between Go structs and Python dataclasses to ensure
they stay in sync. Go structs are the authoritative source for the YAML schema.

Exit code 0: No mismatches found
Exit code 1: Mismatches found (Go fields missing in Python)
"""

import re
import sys
from pathlib import Path
from typing import Dict, Set

# Map Go struct names to Python class names, their file locations, and base classes
# Format: go_name -> (python_name, file_path, base_class_name or None)
TYPE_MAPPINGS = {
    "AWSWorkloadConfig": ("AWSWorkloadConfig", "python-pulumi/src/ptd/aws_workload.py", "WorkloadConfig"),
    "AWSProvisionedVpc": ("AWSProvisionedVPC", "python-pulumi/src/ptd/aws_workload.py", None),
    "SiteConfig": ("AWSSiteConfig", "python-pulumi/src/ptd/aws_workload.py", "SiteConfig"),
    "AzureWorkloadConfig": ("AzureWorkloadConfig", "python-pulumi/src/ptd/azure_workload.py", "WorkloadConfig"),
    "NetworkConfig": ("NetworkConfig", "python-pulumi/src/ptd/azure_workload.py", None),
}

# Base classes to extract (from __init__.py)
BASE_CLASSES = {
    "WorkloadConfig": "python-pulumi/src/ptd/__init__.py",
    "SiteConfig": "python-pulumi/src/ptd/__init__.py",
}

# Python-only fields that are computed/derived (not in YAML)
PYTHON_ONLY_ALLOWLIST = {
    "AWSWorkloadConfig": {
        "db_multi_az",  # Computed property based on environment
        "hosted_zone_id",  # Computed property from sites[MAIN].zone_id
        "autoscaling_enabled",  # Python-specific field
        "existing_flow_log_target_arns",  # Python-specific field
        "nvidia_gpu_enabled",  # Python-specific field
        "vpc_endpoints",  # Python-specific field
        # Base class fields set by _load_common_config() in workload.py
        "environment",  # Set from workload metadata, not from YAML spec
        "network_trust",  # Set from workload metadata, not from YAML spec
        "true_name",  # Set from workload name, not from YAML spec
        "control_room_role_name",  # Optional, set from workload metadata
        "control_room_state_bucket",  # Optional, set from workload metadata
    },
    "AWSSiteConfig": {
        "private_zone",  # Python-specific
        "vpc_associations",  # Python-specific
        "auto_associate_provisioned_vpc",  # Python-specific
        "certificate_validation_enabled",  # Python-specific
    },
    "AzureWorkloadConfig": {
        "root_domain",  # Python-specific
        "ppm_file_share_size_gib",  # Python-specific
        # Base class fields set by _load_common_config() in workload.py
        "environment",  # Set from workload metadata, not from YAML spec
        "network_trust",  # Set from workload metadata, not from YAML spec
        "true_name",  # Set from workload name, not from YAML spec
        "control_room_role_name",  # Optional, set from workload metadata
        "control_room_state_bucket",  # Optional, set from workload metadata
    },
    "NetworkConfig": {
        "bastion_subnet_cidr",  # Python-specific
        "dns_forward_domains",  # Python-specific
        "provisioned_vnet_name",  # Python field name (provisioned_vnet_id in Go)
    },
}

# Go fields that don't exist in Python (field name differences or Go-only)
GO_ONLY_ALLOWLIST = {
    "AWSWorkloadConfig": {
        "hosted_zone_id",  # Go has this in spec, but Python computes it as a property
    },
    "NetworkConfig": {
        "provisioned_vnet_id",  # Go field name; Python uses provisioned_vnet_name
    },
}

# Go structs that represent cluster provisioning state (not YAML config)
# These have extra fields that Python doesn't need to parse from YAML
SKIP_GO_STRUCTS = {
    "AWSWorkloadClusterConfig",  # Cluster provisioning state, not YAML config
    "AzureWorkloadClusterConfig",  # Cluster provisioning state, not YAML config
    "AzureUserNodePoolConfig",  # Cluster provisioning state, not YAML config
    "AzureWorkloadClusterComponentConfig",  # Component versions, Python handles separately
}


def extract_go_yaml_fields(go_file: Path, struct_name: str) -> Set[str]:
    """Extract YAML tag field names from a Go struct."""
    content = go_file.read_text()

    # Find the struct definition
    struct_pattern = rf'type {re.escape(struct_name)} struct {{'
    match = re.search(struct_pattern, content)
    if not match:
        return set()

    # Extract struct body (until closing brace at start of line)
    start_pos = match.end()
    rest = content[start_pos:]

    # Find the closing brace - look for } at start of line
    end_match = re.search(r'\n}', rest)
    if not end_match:
        return set()

    struct_body = rest[:end_match.start()]

    # Extract YAML tags
    # Match patterns like: yaml:"field_name" or yaml:"field_name,omitempty"
    yaml_pattern = r'yaml:"([^,"]+)'
    fields = set()

    for match in re.finditer(yaml_pattern, struct_body):
        field_name = match.group(1)
        fields.add(field_name)

    return fields


def extract_python_dataclass_fields(python_file: Path, class_name: str, base_class_name: str | None = None, repo_root: Path | None = None) -> Set[str]:
    """Extract field names from a Python dataclass, including inherited fields."""
    content = python_file.read_text()

    # Find the class definition - look for @dataclasses.dataclass followed by class
    class_pattern = rf'@dataclasses\.dataclass[^\n]*\nclass {re.escape(class_name)}[:(]'
    match = re.search(class_pattern, content, re.MULTILINE)
    if not match:
        return set()

    # Extract class body (until next class or end of file)
    start_pos = match.end()
    rest = content[start_pos:]

    # Find next class definition or end of file
    next_class = re.search(r'\n(?:@dataclasses\.dataclass|class |def [a-z_])', rest)
    if next_class:
        class_body = rest[:next_class.start()]
    else:
        class_body = rest

    # Extract field names (ignore methods, properties, private fields)
    # Match: field_name: type or field_name: type = default
    field_pattern = r'^\s{4}([a-z_][a-z0-9_]*)\s*:\s*'
    fields = set()

    for match in re.finditer(field_pattern, class_body, re.MULTILINE):
        field_name = match.group(1)
        # Skip if it looks like a method (has def before it on same or previous line)
        # or if it's a property decorator line
        line_start = class_body.rfind('\n', 0, match.start()) + 1
        line = class_body[line_start:match.end()]
        if '@property' in line or 'def ' in line:
            continue
        fields.add(field_name)

    # Include base class fields if specified
    if base_class_name and repo_root and base_class_name in BASE_CLASSES:
        base_file = repo_root / BASE_CLASSES[base_class_name]
        if base_file.exists():
            base_fields = extract_python_dataclass_fields(base_file, base_class_name)
            fields.update(base_fields)

    return fields


def main() -> int:
    """Main validation logic."""
    repo_root = Path(__file__).parent.parent
    go_file = repo_root / "lib" / "types" / "workload.go"

    if not go_file.exists():
        print(f"ERROR: Go file not found: {go_file}")
        return 1

    print("Validating Go ↔ Python config field sync...\n")

    has_errors = False

    for go_struct, (python_class, python_file_rel, base_class_name) in TYPE_MAPPINGS.items():
        # Skip cluster provisioning structs
        if go_struct in SKIP_GO_STRUCTS:
            continue

        python_file = repo_root / python_file_rel

        if not python_file.exists():
            print(f"ERROR: Python file not found: {python_file}")
            has_errors = True
            continue

        go_fields = extract_go_yaml_fields(go_file, go_struct)
        python_fields = extract_python_dataclass_fields(python_file, python_class, base_class_name, repo_root)

        if not go_fields:
            print(f"WARNING: No fields found for Go struct {go_struct}")
            continue

        if not python_fields:
            print(f"WARNING: No fields found for Python class {python_class}")
            continue

        # Fields in Go but missing in Python (potential issues)
        go_only_allowlist = GO_ONLY_ALLOWLIST.get(python_class, set())
        missing_in_python = go_fields - python_fields - go_only_allowlist

        # Fields in Python but not in Go (may be computed/derived)
        python_only_allowlist = PYTHON_ONLY_ALLOWLIST.get(python_class, set())
        python_only = python_fields - go_fields - python_only_allowlist

        if missing_in_python:
            print(f"❌ {go_struct} → {python_class}:")
            print(f"   Fields in Go YAML tags but missing in Python dataclass:")
            for field in sorted(missing_in_python):
                print(f"     - {field}")
            print()
            has_errors = True

        if python_only:
            print(f"⚠️  {go_struct} → {python_class}:")
            print(f"   Fields in Python but not in Go (may be computed/convenience fields):")
            for field in sorted(python_only):
                print(f"     - {field}")
            print(f"   If these are intentional, add them to PYTHON_ONLY_ALLOWLIST")
            print()

        if not missing_in_python and not python_only:
            print(f"✅ {go_struct} → {python_class}: All fields match")

    print()
    if has_errors:
        print("❌ Validation failed: Go fields missing in Python")
        print("   Go structs define the authoritative YAML schema.")
        print("   Python dataclasses must recognize all Go YAML fields.")
        return 1
    else:
        print("✅ All config types are in sync")
        return 0


if __name__ == "__main__":
    sys.exit(main())
