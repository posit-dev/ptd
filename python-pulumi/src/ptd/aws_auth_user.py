import pulumi


class AWSAuthUser:
    def __init__(self, rolearn: pulumi.Input[str], username: str, groups: list[str] | None = None):
        if groups is None:
            groups = []
        self.rolearn: pulumi.Input[str] = rolearn
        self.username: str = username
        self.groups: list[str] = groups

    def to_yaml(self) -> pulumi.Output[str]:
        """
        Turn this AWSAuthUser into a string or Output[string]

        :return: An Output[string]
        """
        rolearn_out = pulumi.Output.from_input(self.rolearn)
        return rolearn_out.apply(lambda x: self._define_aws_auth(x, self.username, self.groups))

    @staticmethod
    def _define_aws_auth(arn: str, username: str, groups: list[str]) -> str:
        """
        Takes string inputs and converts into an aws-auth definition

        :param arn: A Role ARN
        :param username: A kubernetes username to associate with the Role ARN
        :param groups: A list of Kubernetes groups to associate with the Role ARN
        :return: A string representing an aws-auth definition
        """
        result = f"""
- rolearn: {arn}
  username: {username}"""
        if groups:
            result += """
  groups:"""
            for g in groups:
                result += f"""
    - {g}"""

        return result.strip()

    @staticmethod
    def _dedupe_user_list(list_of_users: list["AWSAuthUser"]) -> list["AWSAuthUser"]:
        """
        Deduplicate a list of AWSAuthUsers based on "rolearns"

        We loop over the list, with a "last in wins" approach to rolearn definition. This is done by
        building a dict of the "latest" entries.

        :param list_of_users: A list of AWSAuthUser objects
        :return: A deduplicated list of AWSAuthUser objects
        """

        arn_dedupe: dict[pulumi.Input[str], AWSAuthUser] = {
            user.rolearn: user for user in list_of_users if user is not None
        }

        return list(arn_dedupe.values())

    @staticmethod
    def _dedupe_user_list_input(
        list_of_users: list[pulumi.Input["AWSAuthUser"]],
    ) -> pulumi.Input[list["AWSAuthUser"]]:
        """
        Deduplicate a list of AWSAuthUsers that are pulumi.Input['AWSAuthUser']:raise

        See _dedupe_user_list() for the "pulumi-less" approach

        :param list_of_users: A list of pulumi.Input['AWSAuthUser'] objects
        :return: A deduplicated pulumi.Input[list['AWSAuthUser']]
        """
        user_outputs = [pulumi.Output.from_input(el) for el in list_of_users]
        return pulumi.Output.all(*user_outputs).apply(
            lambda x: AWSAuthUser._dedupe_user_list(x)  # type: ignore
        )

    @staticmethod
    def _loop_over_list(arg: list["AWSAuthUser"]) -> pulumi.Input[str]:
        """
        Loop over a list of AWSAuthUsers, converting each "to_yaml()"

        :param arg: A list of AWSAuthUsers
        :return: A pulumi Input[string] that represents the collective yaml
        """
        output_string: str = ""
        for u in arg:
            output_string = AWSAuthUser._concat_two_input_strings(  # type: ignore
                output_string, u.to_yaml()
            )
        return output_string

    @staticmethod
    def create_configmap_contents(
        users: list[pulumi.Input["AWSAuthUser"]],
    ) -> pulumi.Input[str]:
        """
        Create an AWS Auth Configmap

        :param users: A list of AWSAuthUsers
        :return: A pulumi Input[string]
        """
        dedupe_list_of_users = AWSAuthUser._dedupe_user_list_input(users)
        return pulumi.Output.from_input(dedupe_list_of_users).apply(AWSAuthUser._loop_over_list)

    @staticmethod
    def _append_input(start: str, end: pulumi.Input[str], delimiter: str = "\n") -> pulumi.Input[str]:
        """
        appends a potential Output string to a string

        :param start: A string that starts the output
        :param end: A pulumi Input[string] that will be added to the output
        :param delimiter: A string. The delimiter used between inputs
        :return: A pulumi Input[string]
        """
        addition_output = pulumi.Output.from_input(end)
        return addition_output.apply(lambda x: (start + delimiter + x).strip(delimiter))

    @staticmethod
    def _concat_two_input_strings(
        start: pulumi.Input[str], end: pulumi.Input[str], delimiter: str = "\n"
    ) -> pulumi.Input[str]:
        """
        Aggregates two potential Output strings into a string

        :param start: A pulumi Input[string] that starts the output
        :param end: A pulumi Input[string] that will be added to the output
        :param delimiter: A string. The delimiter used between inputs
        :return: A string or Output[string]
        """
        prefix_output = pulumi.Output.from_input(start)
        return prefix_output.apply(lambda x: AWSAuthUser._append_input(x, end, delimiter))
