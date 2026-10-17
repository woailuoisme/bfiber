-- 电商与商业化商品体系数据库模式定义 (product)
-- 包含单体增强型商品主表，聚合 SKU 编码、类目引用、高精度价格 (NUMERIC)、库存控制、规格参数 (JSONB) 及多态媒体集成

-- ─────────────────────────────────────────────────────────────────────────────
-- 1. 商品核心档案表 ("product")
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE "product" (
  id text NOT NULL DEFAULT uuidv7()::text, -- UUIDv7 主键标识符（PostgreSQL 18 原生生成，Go 侧生成优先）
  sku text NOT NULL, -- 商品唯一货号/库存单位编码（SKU Code）
  name text NOT NULL, -- 商品标题显示名称
  slug text NOT NULL, -- 唯一 URL 别名短链（用于 SEO 与前端路由）
  category_id text NULL, -- 关联商品分类类目 ID
  brand text NULL, -- 商品品牌名称
  price numeric(12, 2) NOT NULL DEFAULT 0.00, -- 商品销售价格（两位小数精度，Go 映射 decimal.Decimal）
  compare_at_price numeric(12, 2) NULL, -- 划线市场参考原价
  cost_price numeric(12, 2) NULL, -- 商品采购成本价
  stock integer NOT NULL DEFAULT 0, -- 当前可用物理库存数量
  safety_stock integer NOT NULL DEFAULT 0, -- 安全库存预警阈值
  status text NOT NULL DEFAULT 'draft', -- 商品发布状态（draft: 草稿, published: 上架在售, archived: 已归档下架）
  image text NULL, -- 商品主图/缩略图直链地址（支持媒体服务首图快速直读）
  description text NULL, -- 商品详细图文介绍说明
  attributes jsonb NOT NULL DEFAULT '{}'::jsonb, -- 自定义规格与扩展属性（颜色、尺码、材质等 JSONB 键值对）
  created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP, -- 记录创建时间戳
  updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP, -- 记录最后更新时间戳
  deleted_at timestamptz NULL, -- 软删除时间戳（为空表示正常活跃商品）
  PRIMARY KEY (id),
  CONSTRAINT ck_product_status CHECK (status IN ('draft', 'published', 'archived'))
);

-- 商品表检索索引与唯一性约束
CREATE UNIQUE INDEX idx_product_sku ON "product" (LOWER(sku)) WHERE deleted_at IS NULL; -- 活跃商品货号唯一索引
CREATE UNIQUE INDEX idx_product_slug ON "product" (LOWER(slug)) WHERE deleted_at IS NULL; -- 活跃商品短链唯一索引
CREATE INDEX idx_product_category_id ON "product" (category_id) WHERE deleted_at IS NULL; -- 类目归类检索索引
CREATE INDEX idx_product_status ON "product" (status) WHERE deleted_at IS NULL; -- 销售状态检索索引
CREATE INDEX idx_product_deleted_at ON "product" (deleted_at); -- 软删除时间索引
