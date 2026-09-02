from ezhiklb_panel.security import hash_password, verify_password


def test_password_hash_roundtrip():
    stored = hash_password("correct horse battery staple")
    assert verify_password("correct horse battery staple", stored) is True
    assert verify_password("wrong password", stored) is False


def test_password_hashes_are_salted():
    a = hash_password("same password")
    b = hash_password("same password")
    assert a != b  # different random salts


def test_verify_password_rejects_malformed_stored_value():
    assert verify_password("anything", "") is False
    assert verify_password("anything", "not-a-valid-format") is False
