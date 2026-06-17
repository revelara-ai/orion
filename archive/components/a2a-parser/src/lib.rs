use serde::{Deserialize, Serialize};
use uuid::Uuid;
use chrono::{DateTime, Utc};
use thiserror::Error;

#[derive(Error, Debug)]
pub enum A2AError {
    #[error("JSON parsing error: {0}")]
    JsonError(#[from] serde_json::Error),
    #[error("Protocol version mismatch: expected {expected}, found {found}")]
    VersionMismatch { expected: String, found: String },
    #[error("Missing required field: {0}")]
    MissingField(String),
    #[error("Invalid field value: {field} - {async_reason}")]
    InvalidValue { field: String, async_reason: String },
}

#[derive(Debug, Serialize, Deserialize, Clone, PartialEq)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum ActionType {
    Analyze,
    Implement,
    Verify,
    Deploy,
    Probe,
}

#[derive(Debug, Serialize, Deserialize, Clone, PartialEq)]
#[serde(rename_all = "lowercase")]
pub enum AssertionStatus {
    Passed,
    Failed,
    Inconclusive,
}

#[derive(Debug, Serialize, Deserialize, Clone, PartialEq)]
#[serde(rename_all = "lowercase")]
pub enum TaskState {
    Completed,
    Error,
    Interrupted,
    RetryRequired,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct A2AHeader {
    pub protocol_version: String,
    pub correlation_id: Uuid,
    pub timestamp: DateTime<Utc>,
    pub sender_id: String,
    pub receiver_id: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct A2AIntent {
    pub action: ActionType,
    pub target_resource: String,
    pub context_ref: Option<String>,
    pub constraints: Option<A2AConstraints>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct A2AConstraints {
    pub max_execution_time: String,
    pub budget_limit: Option<String>,
    pub verification_required: bool,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct A2APayload {
    pub instructions: String,
    pub input_data: serde_json::Value,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct A2AResponseEnvelope {
    pub assertion_status: AssertionStatus,
    pub task_state: TaskState,
    pub error_message: Option<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct A2AVerificationContract {
    pub tier: String,
    pub evidence: serde_json::Value,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct A2APayloadFull {
    pub header: A2AHeader,
    pub intent: A2AIntent,
    pub payload: A2APayload,
    pub response_envelope: Option<A2AResponseEnvelope>,
    pub verification_contract: Option<A2AVerificationContract>,
}

pub struct A2AParser;

impl A2AParser {
    pub fn parse(json_str: &str) -> Result<A2APayloadFull, A2AError> {
        let payload: A2APayloadFull = serde_json::from_str(json_str)?;
        
        if payload.header.protocol_version != "1.0" {
            return Err(A2AError::VersionMismatch {
                expected: "1.0".to_string(),
                found: payload.header.protocol_version.clone(),
            });
        }

        Ok(payload)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_valid_payload() {
        let json = r#"{
            "header": {
                "protocol_version": "1.0",
                "correlation_id": "550e8400-e29b-41d4-a716-446655440000",
                "timestamp": "2024-06-05T10:00:00Z",
                "sender_id": "agent-01",
                "receiver_id": "orchestrator"
            },
            "intent": {
                "action": "ANALYZE",
                "target_resource": "/path/to/file",
                "context_ref": null,
                "constraints": null
            },
            "payload": {
                "instructions": "analyze this",
                "input_data": {}
            },
            "response_envelope": null,
            "verification_contract": null
        }"#;
        let result = A2AParser::parse(json);
        assert!(result.is_ok());
    }

    #[test]
    fn test_parse_invalid_version() {
        let json = r#"{
            "header": {
                "protocol_version": "2.0",
                "correlation_id": "550e8400-e29b-41d4-a716-446655440000",
                "timestamp": "2024-06-05T10:00:00Z",
                "sender_id": "agent-01",
                "receiver_id": "orchestrator"
            },
            "intent": { "action": "ANALYZE", "target_resource": "", "context_ref": null, "constraints": null },
            "payload": { "instructions": "", "input_data": {} },
            "response_envelope": null,
            "verification_contract": null
        }"#;
        let result = A2AParser::parse(json);
        match result {
            Err(A2AError::VersionMismatch { expected, .. }) => assert_eq!(expected, "1.0"),
            _ => panic!("Expected VersionMismatch error"),
        }
    }
}
