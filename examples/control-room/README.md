# Control Room Example

This directory contains an example configuration for a PTD Control Room.

## What is a Control Room?

A Control Room is the central management hub for PTD deployments. It:
- Coordinates workloads across your organization
- Provides centralized authentication and authorization
- Hosts shared services like DNS management

## Configuration

- `ptd.yaml` - Main control room configuration

## Usage

1. Copy this directory to your infrastructure repository:
   ```bash
   cp -r examples/control-room infra/__ctrl__/my-control-room
   ```

2. Edit `ptd.yaml` with your actual values:
   - AWS account ID
   - Domain names
   - Trusted principals (users who can manage the control room)

3. Deploy the control room:
   ```bash
   ptd ensure my-control-room
   ```

## Required Values to Update

| Field | Description |
|-------|-------------|
| `spec.account_id` | Your AWS account ID |
| `spec.domain` | Primary domain for control room services |
| `spec.front_door` | External access domain |
| `spec.trusted_principals` | List of users/roles allowed to manage |
| `spec.resource_tags` | Tags for cost tracking and organization |
