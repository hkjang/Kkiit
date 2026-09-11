-- The category table, its API and the column on talents all existed, but no
-- category was ever created, so the taxonomy was inert. The marketplace filled
-- the gap with eight hardcoded chips that ran a plain text search: pressing
-- 디자인 found listings that happen to contain the word and missed a logo
-- service that never writes it. Navigation that does not do what it says is
-- worse than no navigation, so the taxonomy is seeded and the chips now filter
-- by it.
INSERT INTO categories(id, parent_id, slug, name, description, sort_order) VALUES
	(gen_random_uuid(), NULL, 'design', '디자인', '로고, 웹·앱 화면, 인쇄물까지 시각 결과물', 10),
	(gen_random_uuid(), NULL, 'development', '개발·IT', '웹과 앱, 데이터와 자동화', 20),
	(gen_random_uuid(), NULL, 'media', '영상·사진', '촬영, 편집, 모션그래픽', 30),
	(gen_random_uuid(), NULL, 'marketing', '마케팅', '광고 운영과 콘텐츠 마케팅', 40),
	(gen_random_uuid(), NULL, 'translation', '번역·통역', '번역, 감수, 통역', 50),
	(gen_random_uuid(), NULL, 'content', '문서·콘텐츠', '글쓰기, 기획서, 대본', 60),
	(gen_random_uuid(), NULL, 'consulting', '컨설팅', '사업, 세무·법무, 기술 자문', 70),
	(gen_random_uuid(), NULL, 'education', '교육', '강의, 튜터링, 학습 자료', 80)
ON CONFLICT (slug) DO NOTHING;

INSERT INTO categories(id, parent_id, slug, name, description, sort_order)
SELECT gen_random_uuid(), parent.id, child.slug, child.name, '', child.sort_order
FROM categories parent
JOIN (VALUES
	('design', 'design-brand', '로고·브랜딩', 10),
	('design', 'design-web', '웹·앱 디자인', 20),
	('design', 'design-detail', '상세페이지', 30),
	('design', 'design-print', '인쇄·편집', 40),
	('development', 'dev-web', '웹 개발', 10),
	('development', 'dev-mobile', '모바일 앱', 20),
	('development', 'dev-data', '데이터·AI', 30),
	('development', 'dev-nocode', '노코드·워드프레스', 40),
	('media', 'media-edit', '영상 편집', 10),
	('media', 'media-motion', '모션그래픽', 20),
	('media', 'media-photo', '사진 촬영·보정', 30),
	('marketing', 'marketing-ads', '검색·디스플레이 광고', 10),
	('marketing', 'marketing-social', 'SNS 마케팅', 20),
	('marketing', 'marketing-content', '콘텐츠 마케팅', 30),
	('translation', 'translation-doc', '문서 번역', 10),
	('translation', 'translation-proof', '감수·교정', 20),
	('translation', 'translation-live', '통역', 30),
	('content', 'content-writing', '글쓰기', 10),
	('content', 'content-proposal', '기획서·제안서', 20),
	('content', 'content-script', '대본·스크립트', 30),
	('consulting', 'consulting-business', '사업 전략', 10),
	('consulting', 'consulting-legal', '세무·법무', 20),
	('consulting', 'consulting-tech', '기술 자문', 30),
	('education', 'education-course', '온라인 강의', 10),
	('education', 'education-tutoring', '1:1 튜터링', 20),
	('education', 'education-material', '학습 자료 제작', 30)
) AS child(parent_slug, slug, name, sort_order) ON child.parent_slug = parent.slug
ON CONFLICT (slug) DO NOTHING;

CREATE INDEX IF NOT EXISTS talents_category_idx ON talents(category_id) WHERE category_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS categories_parent_idx ON categories(parent_id);
