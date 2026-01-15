import binascii
import os

import boto3
from pulumi import Input, Output, log
from pulumi.dynamic import CreateResult, Resource, ResourceProvider


class DirectoryServiceDataUserInputs:
    directory_id: Input[str]
    region_name: Input[str]
    email_address: Input[str]
    given_name: Input[str]
    surname: Input[str]
    sam_account_name: Input[str]

    def __init__(self, directory_id, region_name, email_address, given_name, surname, sam_account_name):
        self.directory_id = directory_id
        self.region_name = region_name
        self.email_address = email_address
        self.given_name = given_name
        self.surname = surname
        self.sam_account_name = sam_account_name


class DirectoryServiceDataUserProvider(ResourceProvider):
    def create(self, args) -> CreateResult:
        client = boto3.client("ds-data", region_name=args["region_name"])

        client.create_user(
            DirectoryId=args["directory_id"],
            SAMAccountName=args["sam_account_name"],
            GivenName=args["given_name"],
            Surname=args["surname"],
            EmailAddress=args["email_address"],
        )

        id_ = f"directoryservicedatauser-{binascii.b2a_hex(os.urandom(16)).decode('utf-8')}"
        return CreateResult(id_, outs=args)

    def delete(self, id_, args):
        log.debug(f"deleting user {args['sam_account_name']} ({id_})")
        client = boto3.client("ds-data", region_name=args["region_name"])
        try:
            client.delete_user(DirectoryId=args["directory_id"], SAMAccountName=args["sam_account_name"])
        except client.exceptions.AccessDeniedException as e:
            # in the event of a directory teardown,
            # the ds data service may have been disabled before all users were deleted
            if "DS Data feature is not enabled" in str(e):
                return


class DirectoryServiceDataUser(Resource):
    sam_account_name: Output[str]

    def __init__(self, name: str, props: DirectoryServiceDataUserInputs, opts=None):
        super().__init__(DirectoryServiceDataUserProvider(), name, {**vars(props)}, opts)
