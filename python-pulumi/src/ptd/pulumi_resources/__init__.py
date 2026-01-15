import typing

import pulumi

KustomizeTransformationFunc = typing.Callable[[dict[str, typing.Any], pulumi.ResourceOptions], None]
