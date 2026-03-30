import { describe, expect, it } from "vitest";

import { toIssuedConnectorTokenView } from "./connector-tokens";

describe("connector token helpers", () => {
  it("normalizes issued token payload without exposing secret hash fields", () => {
    const view = toIssuedConnectorTokenView({
      record: {
        connector_id: "agent-local",
        token_id: "agent-local-a1b2",
        token_secret_hash: "should-not-leak",
      },
      plain_token: "dbt_agent-local-a1b2.super-secret",
    });

    expect(view).toEqual({
      connectorID: "agent-local",
      tokenID: "agent-local-a1b2",
      plainToken: "dbt_agent-local-a1b2.super-secret",
      hasIssuedToken: true,
    });
  });
});
