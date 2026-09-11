-- An operator can strip someone's second factor, change what their account is
-- allowed to do, or cut off an integration they depend on, and until now the
-- person it happened to was told none of it. The MFA reset is the one that
-- matters most: quietly removing a target's second factor is a step in taking
-- their account, and the only person who would notice is the person nobody
-- told.
INSERT INTO notification_templates(key, channel, locale, subject_template, body_template) VALUES
	('AccountMFAReset','web','ko-KR','2단계 인증이 해제되었습니다','운영자가 이 계정의 2단계 인증(TOTP)을 해제하고 모든 기기의 로그인을 종료했습니다. 본인이 요청한 것이 아니라면 즉시 비밀번호를 변경하고 문의해 주세요.'),
	('AccountRoleChanged','web','ko-KR','계정 권한이 변경되었습니다','운영자가 이 계정의 역할을 변경했습니다. 지금 역할: {{roles}}'),
	('AccountReactivated','web','ko-KR','계정이 다시 활성화되었습니다','정지되었던 이 계정이 다시 활성화되었습니다.'),
	('APIKeyRevoked','web','ko-KR','API 키가 폐기되었습니다','운영자가 API 키 {{key_name}}을(를) 폐기했습니다. 이 키를 쓰는 연동은 더 이상 동작하지 않습니다.')
ON CONFLICT DO NOTHING;
