/** Local-dev base URL for fulfillment-execution's own REST API. Mirrors
 *  e2e-tests/env.sh's FULFILLMENT_HTTP_PORT=8084. See warehouse-console's
 *  src/config.ts for the note on swapping to runtime config before
 *  multi-environment deployment. */
export const FULFILLMENT_API_BASE = "http://localhost:8084";
