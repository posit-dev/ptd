import typing

import ptd.aws_iam


def test_aws_iam_parse_client_ids_from_frederated_roles() -> None:
    input_roles: list[ptd.aws_iam.AwsRole] = [
        typing.cast(
            ptd.aws_iam.AwsRole,
            {
                "AssumeRolePolicyDocument": {
                    "Statement": {
                        "Principal": {
                            "Federated": "arn:aws:iam::1234567890:oidc-provider/myurl.com",
                        },
                        "Condition": {
                            "StringEquals": {
                                "myurl.com:aud": "client-id",
                            }
                        },
                    },
                }
            },
        ),
        typing.cast(
            ptd.aws_iam.AwsRole,
            {
                "AssumeRolePolicyDocument": {
                    "Statement": {
                        "Principal": {
                            "Federated": "arn:aws:iam::1234567890:oidc-provider/myurl.com",
                        },
                        "Condition": {
                            "StringEquals": {
                                "myurl.com:aud": "client-id-2",
                            }
                        },
                    }
                }
            },
        ),
        typing.cast(
            ptd.aws_iam.AwsRole,
            {
                "AssumeRolePolicyDocument": {
                    "Statement": {
                        "Principal": {
                            "Federated": "arn:aws:iam::1234567890:oidc-provider/oidc.eks.something.amazonaws.com/id/ABCXYZ1234567890",
                        },
                        "Condition": {
                            "StringEquals": {
                                "myurl.com:aud": "should-be-skipped",
                            }
                        },
                    }
                }
            },
        ),
    ]

    output = ptd.aws_iam.aws_iam_parse_client_ids_from_federated_roles(input_roles)
    assert len(output) == 1
    assert len(output["myurl.com"]) == 2
    assert output["myurl.com"] == ["client-id", "client-id-2"]
    output = ptd.aws_iam.aws_iam_parse_client_ids_from_federated_roles([])
    assert len(output) == 0

    bad_inputs: list[ptd.aws_iam.AwsRole] = [
        {},
        typing.cast(ptd.aws_iam.AwsRole, {"AssumeRolePolicyDocument": {}}),
        typing.cast(ptd.aws_iam.AwsRole, {"AssumeRolePolicyDocument": {"Statement": {}}}),
        typing.cast(ptd.aws_iam.AwsRole, {"AssumeRolePolicyDocument": {"Statement": {"Principal": {}}}}),
        typing.cast(ptd.aws_iam.AwsRole, {"AssumeRolePolicyDocument": {"Statement": {"Condition": {}}}}),
    ]
    bad_output = ptd.aws_iam.aws_iam_parse_client_ids_from_federated_roles(bad_inputs)
    assert len(bad_output) == 0


def test_build_hybrid_irsa_role_assume_role_policy() -> None:
    inputs: list[dict[str, typing.Any]] = [
        {
            "namespace": "hello",
            "managed_account_id": "1234",
            "oidc_url_tails": ["tail1", "tail2"],
            "service_accounts": ["sa-one"],
        },
        {
            "namespace": "hello",
            "managed_account_id": "1234",
            "oidc_url_tails": ["tail1", "tail2"],
            "auth_issuers": [
                {
                    "issuer": "https://someurl.com",
                    "client_id": "12345",
                    "emails": ["one@two.com", "two@two.com"],
                    "subs": ["mysub"],
                }
            ],
        },
    ]

    results = [ptd.aws_iam.build_hybrid_irsa_role_assume_role_policy(**i) for i in inputs]

    assert len(results) == 2

    # first result. should be same except for tails
    res = results[0]
    print(res)
    for k, v in [(0, "tail1"), (1, "tail2")]:
        assert res["Statement"][k]["Principal"]["Federated"] == f"arn:aws:iam::1234:oidc-provider/{v}"
        assert res["Statement"][k]["Condition"]["StringEquals"][f"{v}:aud"] == "sts.amazonaws.com"
        assert res["Statement"][k]["Condition"]["StringEquals"][f"{v}:sub"] == ["system:serviceaccount:hello:sa-one"]

    # second result, tests email and subs
    res = results[1]
    print(res)
    for k, v in [(0, "tail1"), (1, "tail2")]:
        assert res["Statement"][k]["Principal"]["Federated"] == f"arn:aws:iam::1234:oidc-provider/{v}"
        assert res["Statement"][k]["Condition"]["StringEquals"][f"{v}:aud"] == "sts.amazonaws.com"
        assert res["Statement"][k]["Condition"]["StringEquals"][f"{v}:sub"] == []

    assert res["Statement"][2]["Principal"]["Federated"] == "arn:aws:iam::1234:oidc-provider/someurl.com"
    assert res["Statement"][2]["Condition"]["StringEquals"]["someurl.com:aud"] == "12345"
    assert res["Statement"][2]["Condition"]["StringEquals"]["someurl.com:email"] == [
        "one@two.com",
        "two@two.com",
    ]
    assert res["Statement"][2]["Condition"]["StringLike"]["someurl.com:sub"] == ["mysub"]

    # check StringEquals types
    for r in results:
        for s in r["Statement"]:
            if "Condition" in s:
                assert isinstance(s["Condition"]["StringEquals"], dict)
