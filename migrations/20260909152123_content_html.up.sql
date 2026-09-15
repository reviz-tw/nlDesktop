-- modify "posts" table
ALTER TABLE "posts" ADD COLUMN "content_html" text NULL, ADD COLUMN "render_version" bigint NULL DEFAULT 0;
