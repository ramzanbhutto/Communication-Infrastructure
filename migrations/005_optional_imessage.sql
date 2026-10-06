-- Optional Mac bridge channel. Existing channel behavior and data remain unchanged.
ALTER TABLE assets DROP CONSTRAINT assets_kind_check;
ALTER TABLE assets ADD CONSTRAINT assets_kind_check CHECK(kind IN ('phone','email','imessage'));
ALTER TABLE delivery_jobs DROP CONSTRAINT delivery_jobs_channel_check;
ALTER TABLE delivery_jobs ADD CONSTRAINT delivery_jobs_channel_check CHECK(channel IN ('sms','call','email','provision','trunk','imessage'));
ALTER TABLE decisions DROP CONSTRAINT decisions_channel_check;
ALTER TABLE decisions ADD CONSTRAINT decisions_channel_check CHECK(channel IN ('sms','call','email','imessage'));
ALTER TABLE channel_permissions DROP CONSTRAINT channel_permissions_channel_check;
ALTER TABLE channel_permissions ADD CONSTRAINT channel_permissions_channel_check CHECK(channel IN ('email','imessage'));
ALTER TABLE infrastructure_inbox DROP CONSTRAINT infrastructure_inbox_channel_check;
ALTER TABLE infrastructure_inbox ADD CONSTRAINT infrastructure_inbox_channel_check CHECK(channel IN ('sms','email','imessage'));
