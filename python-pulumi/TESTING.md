# Testing Guide for PTD Python Pulumi

This guide covers testing patterns and best practices for the PTD Python Pulumi infrastructure code.

## Table of Contents
- [Quick Start](#quick-start)
- [Test Paradigms](#test-paradigms)
- [Shared Fixtures](#shared-fixtures)
- [Required Setup](#required-setup)
- [Running Tests](#running-tests)
- [Writing a New Test](#writing-a-new-test)
- [Common Pitfalls](#common-pitfalls)

## Quick Start

```bash
# Run all tests
just test

# Run specific test file
just test tests/test_paths.py

# Run specific test
just test -k "test_paths_root_with_ptd_root_set"

# Run with coverage
just coverage
```

## Test Paradigms

PTD Python Pulumi tests use two main testing approaches depending on what you're testing.

### 1. Pulumi Mocks (For Infrastructure Resources)

**When to use:** Testing components that create Pulumi resources (AWS, Azure, Kubernetes, etc.)

**How it works:**
- Uses `pulumi.runtime.set_mocks()` to intercept Pulumi resource creation
- Requires implementing `new_resource()` and `call()` methods
- Must use `@pulumi.runtime.test` decorator on test functions
- Allows testing infrastructure code without creating real resources

**Example from codebase:**

```python
import typing
import pulumi
import pulumi_aws as aws
import ptd.pulumi_resources.aws_bucket

class AWSBucketMocks(pulumi.runtime.Mocks):
    def new_resource(self, args: pulumi.runtime.MockResourceArgs) -> tuple[str | None, dict[typing.Any, typing.Any]]:
        # Return resource ID and outputs
        return args.name, dict(args.inputs)

    def call(self, args: pulumi.runtime.MockCallArgs) -> dict[typing.Any, typing.Any]:
        # Mock any function calls
        return {}

pulumi.runtime.set_mocks(AWSBucketMocks(), preview=False)

@pulumi.runtime.test
def test_define_bucket_policy(aws_workload):
    bucket = aws.s3.Bucket("testymctestface-bucket")
    policy = ptd.pulumi_resources.aws_bucket.define_bucket_policy(
        name="testymctestface-policy",
        compound_name=aws_workload.compound_name,
        bucket=bucket,
        policy_name="testymctestface-policy",
        policy_description="For when you need to dip",
        policy_type=ptd.pulumi_resources.aws_bucket.PolicyType.READ,
    )

    def check(args):
        policy_obj: aws.iam.Policy = args[0]
        assert policy_obj is not None

    pulumi.Output.all(policy).apply(check)
```

**Key points:**
- Set mocks at **module level** before defining test functions
- Use `@pulumi.runtime.test` decorator on test functions
- Use `.apply()` to extract values from `Output[T]` for assertions
- Mock class must return appropriate values for your resources

### 2. Direct Mocking (For Logic & Configuration)

**When to use:** Testing pure Python logic that doesn't create Pulumi resources (config parsing, utility functions, data transformations, etc.)

**How it works:**
- Uses standard Python mocking (`unittest.mock`, `pytest.monkeypatch`)
- No Pulumi decorator needed
- Direct function calls and assertions

**Example from codebase:**

```python
from unittest.mock import patch, MagicMock
import ptd

@patch("boto3.Session")
def test_aws_whoami_success(mock_session):
    """Test aws_whoami with successful response."""
    mock_sts_client = MagicMock()
    mock_sts_client.get_caller_identity.return_value = {
        "UserId": "test-user",
        "Account": "123456789012",
        "Arn": "arn:aws:iam::123456789012:user/test-user",
    }
    mock_session.return_value.client.return_value = mock_sts_client

    identity, success = ptd.aws_whoami()

    assert success is True
    assert identity["Account"] == "123456789012"
```

**Key points:**
- No `@pulumi.runtime.test` decorator
- Use `@patch` for mocking external dependencies
- Direct assertions on return values
- Can use `monkeypatch` fixture for environment variables

## Shared Fixtures

The `conftest.py` file provides shared fixtures that eliminate duplication across tests.

### `ptd_root` - Environment Setup

Sets `PTD_ROOT` environment variable to a temporary directory.

```python
def test_paths_root_with_ptd_root_set(ptd_root):
    """PTD_ROOT is automatically set to ptd_root."""
    paths = Paths()
    assert paths.root == ptd_root
```

**When to use:** Any test that loads workload configs or uses the `Paths` class.

### `pulumi_mocks` - Standard Pulumi Mocks

Provides a standard Pulumi mock class that echoes inputs as outputs.

```python
@pulumi.runtime.test
def test_my_resource(pulumi_mocks):
    pulumi.runtime.set_mocks(pulumi_mocks(), preview=False)
    # Test Pulumi resources here
```

**When to use:** When you need basic Pulumi mocking and don't require custom mock behavior.

**Note:** For tests requiring specific mock behavior (returning particular resource properties, simulating API calls), create a custom mock class in your test file.

### `aws_workload` - Mock AWS Workload

Pre-configured `AWSWorkload` object with test data.

```python
def test_something(aws_workload):
    assert aws_workload.cfg.environment == "test"
    assert aws_workload.cfg.region == "useast1"
```

**Configuration:**
- Name: `testing01-test`
- Environment: `test`
- Region: `useast1`
- Account: `9001`
- Single cluster: `19551105`
- Single site: `main` with domain `puppy.party`

### `azure_workload` - Mock Azure Workload

Pre-configured `AzureWorkload` object with test data.

```python
def test_something(azure_workload):
    assert azure_workload.cfg.environment == "test"
    assert azure_workload.cfg.region == "eastus"
```

**Configuration:**
- Name: `testing01-test`
- Environment: `test`
- Region: `eastus`
- Single cluster: `19551105`
- Single site: `main` with domain `puppy.party`
- Standard network configuration

## Required Setup

### PTD_ROOT Environment Variable

**CRITICAL:** Most PTD code requires `PTD_ROOT` to be set. This points to the targets configuration directory.

**Three ways to set it:**

1. **Use `ptd_root` fixture** (recommended for most tests):
   ```python
   def test_something(ptd_root):
       # PTD_ROOT is automatically set
       paths = Paths()
   ```

2. **Use `monkeypatch` directly**:
   ```python
   def test_something(monkeypatch):
       monkeypatch.setenv("PTD_ROOT", "/path/to/targets")
       paths = Paths()
   ```

3. **Use `aws_workload` or `azure_workload` fixtures** (sets `PTD_ROOT` automatically):
   ```python
   def test_something(aws_workload):
       # PTD_ROOT is automatically set via the fixture
       assert aws_workload.cfg.environment == "test"
   ```

## Running Tests

### Basic Commands

```bash
# Run all tests
just test

# Run all tests (verbose)
just test -v

# Run specific test file
just test tests/test_paths.py

# Run specific test by name
just test -k "test_paths_root"

# Run tests matching a pattern
just test -k "workload"

# Run with coverage
just coverage

# Run and show coverage report
just coverage
cat coverage.json | jq '.totals.percent_covered'
```

### Test Output

```bash
# Show print statements
just test -s

# Show detailed test output
just test -vv

# Stop on first failure
just test -x

# Run last failed tests
just test --lf
```

### Pytest Options

All pytest options work with `just test`:

```bash
# Run with warnings
just test -W all

# Parallel execution (requires pytest-xdist)
just test -n auto

# Show slowest tests
just test --durations=10
```

## Writing a New Test

### Step 1: Choose the Right Paradigm

Ask yourself: **Does my code create Pulumi resources?**

- **YES** → Use Pulumi Mocks (see [Template A](#template-a-pulumi-resource-test))
- **NO** → Use Direct Mocking (see [Template B](#template-b-logic-test))

### Template A: Pulumi Resource Test

```python
"""Tests for [component name]."""

import typing
import pulumi
import ptd

class MyComponentMocks(pulumi.runtime.Mocks):
    """Mock Pulumi resource calls for testing."""

    def new_resource(
        self, args: pulumi.runtime.MockResourceArgs
    ) -> tuple[str | None, dict[typing.Any, typing.Any]]:
        # Return resource ID and outputs
        return args.name, dict(args.inputs)

    def call(
        self, args: pulumi.runtime.MockCallArgs
    ) -> dict[typing.Any, typing.Any]:
        # Mock function calls
        return {}

# Set mocks at module level
pulumi.runtime.set_mocks(MyComponentMocks(), preview=False)

@pulumi.runtime.test
def test_my_component_creates_resource(aws_workload):
    """Test that my component creates expected resources."""
    result = ptd.my_module.create_something(
        name="test-resource",
        config=aws_workload.cfg,
    )

    def check(args):
        resource = args[0]
        assert resource is not None
        # Add your assertions here

    pulumi.Output.all(result).apply(check)
```

### Template B: Logic Test

```python
"""Tests for [module name]."""

import pytest
from unittest.mock import patch, MagicMock
import ptd

def test_my_function_success(ptd_root):
    """Test my function with successful case."""
    result = ptd.my_module.my_function("input")

    assert result == "expected output"

def test_my_function_with_workload(aws_workload):
    """Test my function with workload config."""
    result = ptd.my_module.my_function(aws_workload.cfg)

    assert result.some_property == "expected"

@patch("ptd.my_module.external_dependency")
def test_my_function_with_mock(mock_dependency):
    """Test my function with mocked dependency."""
    mock_dependency.return_value = "mocked value"

    result = ptd.my_module.my_function()

    assert result == "mocked value"
    mock_dependency.assert_called_once()
```

### Step 2: Organize Your Tests

```python
class TestMyFeature:
    """Group related tests together."""

    def test_default_behavior(self):
        """Test with defaults."""
        pass

    def test_custom_config(self):
        """Test with custom configuration."""
        pass

    def test_error_handling(self):
        """Test error cases."""
        pass
```

### Step 3: Write Descriptive Tests

**Good test names:**
- `test_vpc_endpoints_config_default_initialization`
- `test_aws_whoami_success`
- `test_paths_root_without_ptd_root_raises_error`

**Bad test names:**
- `test_config`
- `test_1`
- `test_it_works`

## Common Pitfalls

### 1. Forgetting to Set PTD_ROOT

**Problem:**
```python
def test_load_workload():
    wl = AWSWorkload(name="test01-staging")  # RuntimeError: PTD_ROOT not set
```

**Solution:**
```python
def test_load_workload(ptd_root):
    wl = AWSWorkload(name="test01-staging")  # PTD_ROOT is set via fixture
```

### 2. Using the Wrong Mock Paradigm

**Problem:** Using Pulumi mocks for non-Pulumi code:
```python
# DON'T DO THIS
class Mocks(pulumi.runtime.Mocks):
    # ...

@pulumi.runtime.test
def test_parse_config():  # This doesn't create Pulumi resources!
    result = ptd.parse_yaml_config("test.yaml")
```

**Solution:** Use direct testing:
```python
def test_parse_config(ptd_root):
    result = ptd.parse_yaml_config("test.yaml")
    assert result["environment"] == "test"
```

### 3. Not Handling Pulumi Output[T]

**Problem:**
```python
@pulumi.runtime.test
def test_bucket_name():
    bucket = aws.s3.Bucket("my-bucket")
    assert bucket.id == "my-bucket"  # This will fail! bucket.id is Output[str]
```

**Solution:** Use `.apply()` to extract values:
```python
@pulumi.runtime.test
def test_bucket_name():
    bucket = aws.s3.Bucket("my-bucket")

    def check(bucket_id):
        assert bucket_id == "my-bucket"

    bucket.id.apply(check)
```

### 4. Testing Private Methods Directly

**Problem:**
```python
def test_private_method():
    obj = MyClass()
    result = obj._internal_helper()  # Testing implementation details
```

**Solution:** Test through the public API:
```python
def test_public_method():
    obj = MyClass()
    result = obj.public_method()  # This internally uses _internal_helper
    assert result == "expected"
```

**Exception:** It's acceptable to test private methods when they contain complex logic that needs thorough testing independently of the public API. See `test_grafana_alloy.py` for examples.

### 5. Not Cleaning Up Test Data

**Problem:**
```python
def test_create_file():
    Path("/tmp/test.txt").write_text("data")  # File persists after test
```

**Solution:** Use `tmp_path` fixture:
```python
def test_create_file(tmp_path):
    test_file = tmp_path / "test.txt"
    test_file.write_text("data")
    # tmp_path is automatically cleaned up
```

### 6. Module-Level Mocks in Wrong Place

**Problem:**
```python
@pulumi.runtime.test
def test_resource():
    pulumi.runtime.set_mocks(Mocks(), preview=False)  # Too late!
```

**Solution:** Set mocks at module level:
```python
class Mocks(pulumi.runtime.Mocks):
    # ...

pulumi.runtime.set_mocks(Mocks(), preview=False)  # Before any test functions

@pulumi.runtime.test
def test_resource():
    # Mocks are already set
```

### 7. Sharing Mutable Test Data

**Problem:**
```python
SHARED_CONFIG = {"setting": "value"}

def test_one():
    SHARED_CONFIG["setting"] = "changed"  # Affects test_two!

def test_two():
    assert SHARED_CONFIG["setting"] == "value"  # Fails if test_one runs first
```

**Solution:** Use fixtures or create fresh data in each test:
```python
@pytest.fixture
def config():
    return {"setting": "value"}

def test_one(config):
    config["setting"] = "changed"  # Fresh copy for each test

def test_two(config):
    assert config["setting"] == "value"  # Gets fresh copy
```

## Examples from Codebase

### Testing Configuration Parsing
See: `tests/test_ptd_init.py`
- Uses direct mocking
- Tests dataclasses and configuration logic
- No Pulumi resources involved

### Testing Infrastructure Resources
See: `tests/test_ptd_pulumi_resources_aws_bucket.py`
- Uses Pulumi mocks
- Tests resource creation
- Uses `.apply()` for assertions

### Testing with Workload Fixtures
See: `tests/test_azure_bastion_config.py`
- Uses `aws_workload` and `azure_workload` fixtures
- Tests configuration defaults
- Compares AWS and Azure patterns

### Testing Complex Logic
See: `tests/test_aws_iam.py`
- Tests policy generation logic
- Uses type casting for complex data structures
- No Pulumi mocks needed

## Tips and Best Practices

1. **Keep tests focused**: One test should verify one behavior
2. **Use descriptive names**: Test names should describe what they verify
3. **Test edge cases**: Don't just test the happy path
4. **Use fixtures liberally**: Avoid duplicating setup code
5. **Mock external dependencies**: Tests should not make real API calls
6. **Verify behavior, not implementation**: Test what the code does, not how it does it
7. **Run tests frequently**: Use `just test -k <test_name>` during development

## Further Reading

- [Pytest Documentation](https://docs.pytest.org/)
- [Pulumi Testing Guide](https://www.pulumi.com/docs/using-pulumi/testing/)
- [unittest.mock Documentation](https://docs.python.org/3/library/unittest.mock.html)
