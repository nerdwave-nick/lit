-- lit storage schema version 4: the workflow stage of a client.
--
-- stage is the outer workflow skill the client is running (impl, planner,
-- ...), NULL when none is set. It is a display label, not a closed
-- vocabulary: the schema accepts any lowercase word of letters, digits and
-- hyphens of up to 32 characters, and the service enforces the exact shape.
-- It changes with the client's other properties and is never history.

ALTER TABLE clients ADD COLUMN stage TEXT CHECK (stage IS NULL OR (length(stage) BETWEEN 1 AND 32 AND stage NOT GLOB '*[^a-z0-9-]*'));
