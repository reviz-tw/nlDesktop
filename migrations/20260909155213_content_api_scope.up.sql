-- modify "api_keys" table
ALTER TABLE "api_keys" ADD COLUMN "scope" character varying NOT NULL DEFAULT 'cms';
