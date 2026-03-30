import { asRecord, readText } from "./records";
import type { ApiRecord } from "./types";

export type IssuedConnectorTokenView = {
  connectorID: string;
  tokenID: string;
  plainToken: string;
  hasIssuedToken: boolean;
};

export function toIssuedConnectorTokenView(result: unknown): IssuedConnectorTokenView {
  const resultRecord = asRecord(result);
  const issuedRecord = asRecord(resultRecord.record);

  const plainToken = readText(resultRecord, "plain_token", "");
  const connectorID = readText(issuedRecord, "connector_id", "--");
  const tokenID = readText(issuedRecord, "token_id", "--");

  return {
    connectorID,
    tokenID,
    plainToken,
    hasIssuedToken: plainToken !== "",
  };
}

export function readIssuedConnectorTokenRecord(result: unknown): ApiRecord {
  return asRecord(asRecord(result).record);
}
