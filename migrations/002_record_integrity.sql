-- Historical records are inserted once. Only the isolated demo reset deletes them.
REVOKE UPDATE ON decisions,audit,operations,messages,replies FROM ops_app;
