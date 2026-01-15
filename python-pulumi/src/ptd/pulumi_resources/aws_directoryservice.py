import binascii
import os
from time import sleep

import boto3
from pulumi import Input, Output, log
from pulumi.dynamic import CreateResult, Resource, ResourceProvider


class DirectoryServiceDirectoryDataAccessEnabledInputs:
    directory_id: Input[str]
    region_name: Input[str]

    def __init__(self, directory_id, region_name):
        self.directory_id = directory_id
        self.region_name = region_name


def data_access_status(client, directory_id) -> str:
    status = client.describe_directory_data_access(DirectoryId=directory_id)
    return status["DataAccessStatus"]


class DirectoryServiceDirectoryDataAccessEnabledProvider(ResourceProvider):
    def create(self, args) -> CreateResult:
        client = boto3.client("ds", region_name=args["region_name"])
        status = data_access_status(client, args["directory_id"])

        if status not in ["Enabled", "Enabling"]:
            client.enable_directory_data_access(DirectoryId=args["directory_id"])

        while data_access_status(client, args["directory_id"]) != "Enabled":
            sleep(2)

        id_ = f"directoryservicedirectorydataaccessenabled-{binascii.b2a_hex(os.urandom(16)).decode('utf-8')}"
        return CreateResult(id_, outs=args)

    def delete(self, id_, args):
        log.debug(f"disabling directory data access ({id_})")
        client = boto3.client("ds", region_name=args["region_name"])
        status = data_access_status(client, args["directory_id"])
        if status in ["Enabled"]:
            client.disable_directory_data_access(DirectoryId=args["directory_id"])


class DirectoryServiceDirectoryDataAccessEnabled(Resource):
    directory_id: Output[str]

    def __init__(self, name: str, props: DirectoryServiceDirectoryDataAccessEnabledInputs, opts=None):
        super().__init__(DirectoryServiceDirectoryDataAccessEnabledProvider(), name, {**vars(props)}, opts)
