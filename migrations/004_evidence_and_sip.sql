ALTER TABLE decisions DROP CONSTRAINT decisions_channel_check;
ALTER TABLE decisions ADD CONSTRAINT decisions_channel_check CHECK(channel IN ('sms','call','email'));
ALTER TABLE delivery_jobs ADD COLUMN decision_id text;
ALTER TABLE delivery_jobs ADD CONSTRAINT delivery_decision_fk FOREIGN KEY(workspace_id,decision_id) REFERENCES decisions(workspace_id,id);
CREATE TABLE infrastructure_sip (
 workspace_id text PRIMARY KEY REFERENCES workspaces(id), observation jsonb NOT NULL,
 checked_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE infrastructure_sip ENABLE ROW LEVEL SECURITY;
ALTER TABLE infrastructure_sip FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_scope ON infrastructure_sip USING(workspace_id=current_setting('app.workspace_id',true)) WITH CHECK(workspace_id=current_setting('app.workspace_id',true));
GRANT SELECT,INSERT,UPDATE,DELETE ON infrastructure_sip TO ops_app;
