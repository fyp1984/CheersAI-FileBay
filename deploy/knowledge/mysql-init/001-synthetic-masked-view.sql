-- 仅用于内部试用的合成数据。表中不得放入真实客户、员工或业务原文。
CREATE TABLE source_faq_masked (
  record_id BIGINT NOT NULL PRIMARY KEY,
  question VARCHAR(255) NOT NULL,
  answer_markdown TEXT NOT NULL,
  updated_at DATETIME NOT NULL
);

INSERT INTO source_faq_masked (record_id, question, answer_markdown, updated_at) VALUES
  (1, '如何申请内部知识库访问权限？', '请在 FileBay 中提交访问申请，由空间负责人按资料密级审批。', '2026-07-01 09:00:00'),
  (2, '知识文档到期后会怎样？', '系统会停止检索返回，并由管理员执行到期检查后下架并删除派生索引。', '2026-07-02 09:00:00');

-- FileBay 连接器只接受 v_kb_masked_ 前缀的视图，拒绝任意原始表或任意 SQL。
CREATE VIEW v_kb_masked_faq AS
SELECT record_id, question, answer_markdown, updated_at
FROM source_faq_masked;

CREATE USER 'knowledge_reader'@'%' IDENTIFIED BY 'trial_only_reader_password';
GRANT SELECT ON knowledge_source.v_kb_masked_faq TO 'knowledge_reader'@'%';
FLUSH PRIVILEGES;
