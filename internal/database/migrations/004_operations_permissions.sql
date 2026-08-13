INSERT INTO permissions(code, name, description) VALUES
('risk.manage','위험 관리','위험 신호와 분쟁 예외 대기열 조회 및 처리')
ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name, description=EXCLUDED.description;

INSERT INTO role_permissions(role_code, permission_code) VALUES
('super_admin','risk.manage'),
('operator','risk.manage')
ON CONFLICT DO NOTHING;
