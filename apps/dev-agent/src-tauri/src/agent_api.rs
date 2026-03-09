use reqwest::Client;
use serde::de::DeserializeOwned;
use serde_json::{json, Value};

#[derive(Clone)]
pub struct AgentApiClient {
    base_url: String,
    http: Client,
}

impl AgentApiClient {
    pub fn new(base_url: String) -> Self {
        Self {
            base_url,
            http: Client::new(),
        }
    }

    pub async fn get_json<T>(&self, path: &str) -> Result<T, String>
    where
        T: DeserializeOwned,
    {
        let url = format!("{}{}", self.base_url, path);
        let response = self
            .http
            .get(url)
            .send()
            .await
            .map_err(|err| format!("agent request failed: {err}"))?;

        if !response.status().is_success() {
            return Err(format!(
                "agent request failed with status {}",
                response.status()
            ));
        }

        response
            .json::<T>()
            .await
            .map_err(|err| format!("decode response failed: {err}"))
    }

    pub async fn post_json<T>(&self, path: &str) -> Result<T, String>
    where
        T: DeserializeOwned,
    {
        let url = format!("{}{}", self.base_url, path);
        let response = self
            .http
            .post(url)
            .json(&json!({}))
            .send()
            .await
            .map_err(|err| format!("agent request failed: {err}"))?;

        if !response.status().is_success() {
            return Err(format!(
                "agent request failed with status {}",
                response.status()
            ));
        }

        response
            .json::<T>()
            .await
            .map_err(|err| format!("decode response failed: {err}"))
    }

    pub async fn delete_json<T>(&self, path: &str) -> Result<T, String>
    where
        T: DeserializeOwned,
    {
        let url = format!("{}{}", self.base_url, path);
        let response = self
            .http
            .delete(url)
            .send()
            .await
            .map_err(|err| format!("agent request failed: {err}"))?;

        if !response.status().is_success() {
            return Err(format!(
                "agent request failed with status {}",
                response.status()
            ));
        }

        response
            .json::<T>()
            .await
            .map_err(|err| format!("decode response failed: {err}"))
    }

    pub async fn state_summary(&self) -> Result<Value, String> {
        self.get_json("/api/v1/state/summary").await
    }

    pub async fn tunnel_state(&self) -> Result<Value, String> {
        self.get_json("/api/v1/state/tunnel").await
    }

    pub async fn diagnostics(&self) -> Result<Value, String> {
        self.get_json("/api/v1/state/diagnostics").await
    }

    pub async fn registrations(&self) -> Result<Vec<Value>, String> {
        self.get_json("/api/v1/registrations").await
    }

    pub async fn recent_errors(&self) -> Result<Vec<Value>, String> {
        self.get_json("/api/v1/state/errors").await
    }

    pub async fn recent_requests(&self) -> Result<Vec<Value>, String> {
        self.get_json("/api/v1/state/requests").await
    }

    pub async fn active_intercepts(&self) -> Result<Vec<Value>, String> {
        self.get_json("/api/v1/state/intercepts").await
    }

    pub async fn unregister_registration(&self, instance_id: &str) -> Result<Value, String> {
        let path = format!("/api/v1/registrations/{}", instance_id);
        self.delete_json(&path).await
    }

    pub async fn reconnect(&self) -> Result<Value, String> {
        self.post_json("/api/v1/control/reconnect").await
    }
}
