window.OPENAPI_SPEC = {
  "openapi": "3.0.3",
  "info": {
    "title": "Agent API",
    "version": "1.0.0",
    "description": "Single-operator agent. No authentication: reachability over the tailnet is the only access control. A WebSocket at /ws streams transcript entries, token deltas, job ticks, and breaker changes; every state change delivered there is also retrievable from the endpoints below."
  },
  "servers": [
    {
      "url": "https://agent.tailnet.ts.net",
      "description": "Tailscale Serve"
    }
  ],
  "tags": [
    {
      "name": "sessions"
    },
    {
      "name": "jobs"
    },
    {
      "name": "memory"
    },
    {
      "name": "tools"
    },
    {
      "name": "skills"
    },
    {
      "name": "system"
    }
  ],
  "paths": {
    "/sessions": {
      "get": {
        "tags": [
          "sessions"
        ],
        "summary": "List sessions, most recently active first.",
        "parameters": [
          {
            "name": "status",
            "in": "query",
            "schema": {
              "type": "string",
              "enum": [
                "active",
                "archived"
              ]
            }
          },
          {
            "name": "limit",
            "in": "query",
            "schema": {
              "type": "integer",
              "default": 50
            }
          }
        ],
        "responses": {
          "200": {
            "description": "Sessions.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "$ref": "#/components/schemas/Session"
                  }
                }
              }
            }
          }
        }
      },
      "post": {
        "tags": [
          "sessions"
        ],
        "summary": "Create a session with its configuration.",
        "requestBody": {
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "properties": {
                  "title": {
                    "type": "string"
                  },
                  "model": {
                    "type": "string",
                    "description": "Omitted, it is inherited from the seed session, then from the most recently used one, then from the configured default."
                  },
                  "enabled_tools": {
                    "type": "array",
                    "items": {
                      "type": "string"
                    },
                    "description": "Tool names defined in this session's prompt. Three states: omit it to inherit, send null for every one, send a list for exactly those."
                  },
                  "enabled_skills": {
                    "type": "array",
                    "items": {
                      "type": "string"
                    },
                    "description": "Skill names indexed in this session's prompt. Three states: omit it to inherit, send null for every one, send a list for exactly those."
                  },
                  "from": {
                    "type": "string",
                    "description": "Seed the new session's configuration from that session."
                  }
                }
              }
            }
          }
        },
        "responses": {
          "201": {
            "description": "Created.",
            "content": {
              "application/json": {
                "schema": {
                  "$ref": "#/components/schemas/Session"
                }
              }
            }
          }
        },
        "description": "A session's model, tools, and skills are fixed once it exists. They are chosen here, or inherited, and afterwards change only by rotating."
      }
    },
    "/sessions/{id}": {
      "parameters": [
        {
          "$ref": "#/components/parameters/Id"
        }
      ],
      "get": {
        "tags": [
          "sessions"
        ],
        "summary": "Get a session.",
        "responses": {
          "200": {
            "description": "Session.",
            "content": {
              "application/json": {
                "schema": {
                  "$ref": "#/components/schemas/Session"
                }
              }
            }
          },
          "404": {
            "$ref": "#/components/responses/NotFound"
          }
        },
        "description": "A session is never superseded, so an identifier always addresses the conversation it named."
      },
      "patch": {
        "tags": [
          "sessions"
        ],
        "summary": "Update title or status.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "properties": {
                  "title": {
                    "type": "string"
                  },
                  "status": {
                    "type": "string",
                    "enum": [
                      "active",
                      "archived"
                    ]
                  }
                }
              }
            }
          }
        },
        "responses": {
          "200": {
            "description": "Updated.",
            "content": {
              "application/json": {
                "schema": {
                  "$ref": "#/components/schemas/Session"
                }
              }
            }
          },
          "409": {
            "description": "Refused: the body carried model, enabled_tools, or enabled_skills. A session's configuration is fixed for its life; POST /sessions/{id}/fork to copy the conversation into a new session under a new one.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "error": {
                      "type": "string"
                    }
                  }
                }
              }
            }
          }
        },
        "description": "This endpoint changes nothing that shapes the system prompt, so it can never invalidate a cached prefix. Model, tools, and skills are not accepted here at all: they are fixed for the life of a session and move only through a rotation."
      },
      "delete": {
        "tags": [
          "sessions"
        ],
        "summary": "Delete a session, its transcript, and its jobs.",
        "responses": {
          "204": {
            "description": "Deleted."
          }
        }
      }
    },
    "/sessions/{id}/transcript": {
      "parameters": [
        {
          "$ref": "#/components/parameters/Id"
        }
      ],
      "get": {
        "tags": [
          "sessions"
        ],
        "summary": "Read transcript entries. Returns the complete stored record, including entries not sent to the model.",
        "parameters": [
          {
            "name": "before",
            "in": "query",
            "description": "Entry sequence number to page backwards from.",
            "schema": {
              "type": "integer"
            }
          },
          {
            "name": "limit",
            "in": "query",
            "schema": {
              "type": "integer",
              "default": 100
            }
          },
          {
            "name": "include_events",
            "in": "query",
            "schema": {
              "type": "boolean",
              "default": true
            }
          }
        ],
        "responses": {
          "200": {
            "description": "Entries, oldest first.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "$ref": "#/components/schemas/Entry"
                  }
                }
              }
            }
          }
        }
      }
    },
    "/sessions/{id}/messages": {
      "parameters": [
        {
          "$ref": "#/components/parameters/Id"
        }
      ],
      "post": {
        "tags": [
          "sessions"
        ],
        "summary": "Send a user message. Queues behind any turn already running in this session.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": [
                  "text"
                ],
                "properties": {
                  "text": {
                    "type": "string"
                  },
                  "attachments": {
                    "type": "array",
                    "items": {
                      "type": "string"
                    },
                    "description": "Upload ids."
                  }
                }
              }
            }
          }
        },
        "responses": {
          "202": {
            "description": "Accepted; results stream over /ws.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "entry_seq": {
                      "type": "integer"
                    }
                  }
                }
              }
            }
          }
        }
      }
    },
    "/search": {
      "get": {
        "tags": [
          "sessions"
        ],
        "summary": "Full-text search across all transcripts.",
        "parameters": [
          {
            "name": "q",
            "in": "query",
            "required": true,
            "schema": {
              "type": "string"
            }
          },
          {
            "name": "limit",
            "in": "query",
            "schema": {
              "type": "integer",
              "default": 20
            }
          }
        ],
        "responses": {
          "200": {
            "description": "Matches.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": {
                      "session_id": {
                        "type": "string"
                      },
                      "session_title": {
                        "type": "string"
                      },
                      "entry_seq": {
                        "type": "integer"
                      },
                      "snippet": {
                        "type": "string"
                      },
                      "created_at": {
                        "type": "string",
                        "format": "date-time"
                      }
                    }
                  }
                }
              }
            }
          }
        }
      }
    },
    "/jobs": {
      "get": {
        "tags": [
          "jobs"
        ],
        "summary": "List jobs.",
        "parameters": [
          {
            "name": "session_id",
            "in": "query",
            "schema": {
              "type": "string"
            }
          }
        ],
        "responses": {
          "200": {
            "description": "Jobs.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "$ref": "#/components/schemas/Job"
                  }
                }
              }
            }
          }
        }
      },
      "post": {
        "tags": [
          "jobs"
        ],
        "summary": "Create a job attached to a session.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "$ref": "#/components/schemas/JobSpec"
              }
            }
          }
        },
        "responses": {
          "201": {
            "description": "Created.",
            "content": {
              "application/json": {
                "schema": {
                  "$ref": "#/components/schemas/Job"
                }
              }
            }
          },
          "422": {
            "description": "Rejected: the schedule, the prompt, or after_acting is not valid."
          }
        }
      },
      "delete": {
        "tags": [
          "jobs"
        ],
        "summary": "Delete every finished job, with its run log. The only bulk delete: status must be `done`, so nothing still scheduled can be removed this way.",
        "parameters": [
          {
            "name": "status",
            "in": "query",
            "required": true,
            "description": "The state to delete. Only `done` is accepted.",
            "schema": {
              "type": "string",
              "enum": [
                "done"
              ]
            }
          },
          {
            "name": "session_id",
            "in": "query",
            "description": "Clear only this session's finished jobs.",
            "schema": {
              "type": "string"
            }
          }
        ],
        "responses": {
          "200": {
            "description": "How many jobs were deleted.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "deleted": {
                      "type": "integer"
                    }
                  },
                  "required": [
                    "deleted"
                  ]
                }
              }
            }
          },
          "400": {
            "description": "The status was missing or was not `done`; nothing was deleted.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "error": {
                      "type": "string"
                    }
                  }
                }
              }
            }
          }
        }
      }
    },
    "/jobs/{id}": {
      "parameters": [
        {
          "$ref": "#/components/parameters/Id"
        }
      ],
      "get": {
        "tags": [
          "jobs"
        ],
        "summary": "Get a job.",
        "responses": {
          "200": {
            "description": "Job.",
            "content": {
              "application/json": {
                "schema": {
                  "$ref": "#/components/schemas/Job"
                }
              }
            }
          },
          "404": {
            "$ref": "#/components/responses/NotFound"
          }
        }
      },
      "patch": {
        "tags": [
          "jobs"
        ],
        "summary": "Update a job.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "$ref": "#/components/schemas/JobSpec"
              }
            }
          }
        },
        "responses": {
          "200": {
            "description": "Updated.",
            "content": {
              "application/json": {
                "schema": {
                  "$ref": "#/components/schemas/Job"
                }
              }
            }
          }
        }
      },
      "delete": {
        "tags": [
          "jobs"
        ],
        "summary": "Delete a job. Its run log goes with it.",
        "responses": {
          "204": {
            "description": "Deleted."
          }
        }
      }
    },
    "/memory": {
      "get": {
        "tags": [
          "memory"
        ],
        "summary": "List memory items and capacity usage.",
        "responses": {
          "200": {
            "description": "Memory.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "items": {
                      "type": "array",
                      "items": {
                        "$ref": "#/components/schemas/MemoryItem"
                      }
                    },
                    "used": {
                      "type": "integer"
                    },
                    "capacity": {
                      "type": "integer"
                    }
                  }
                }
              }
            }
          }
        }
      },
      "post": {
        "tags": [
          "memory"
        ],
        "summary": "Add an item.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": [
                  "text"
                ],
                "properties": {
                  "text": {
                    "type": "string"
                  }
                }
              }
            }
          }
        },
        "responses": {
          "201": {
            "description": "Stored."
          },
          "409": {
            "description": "Capacity exceeded. Nothing was stored; the body lists current items so they can be consolidated.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "error": {
                      "type": "string"
                    },
                    "items": {
                      "type": "array",
                      "items": {
                        "$ref": "#/components/schemas/MemoryItem"
                      }
                    }
                  }
                }
              }
            }
          }
        }
      }
    },
    "/memory/{id}": {
      "parameters": [
        {
          "$ref": "#/components/parameters/Id"
        }
      ],
      "patch": {
        "tags": [
          "memory"
        ],
        "summary": "Edit an item.",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "properties": {
                  "text": {
                    "type": "string"
                  }
                }
              }
            }
          }
        },
        "responses": {
          "200": {
            "description": "Updated."
          }
        }
      },
      "delete": {
        "tags": [
          "memory"
        ],
        "summary": "Delete an item.",
        "responses": {
          "204": {
            "description": "Deleted."
          }
        }
      }
    },
    "/tools": {
      "get": {
        "tags": [
          "tools"
        ],
        "summary": "List loaded tools and any that failed validation.",
        "responses": {
          "200": {
            "description": "Tools.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "loaded": {
                      "type": "array",
                      "items": {
                        "$ref": "#/components/schemas/Tool"
                      }
                    },
                    "failed": {
                      "type": "array",
                      "items": {
                        "type": "object",
                        "properties": {
                          "name": {
                            "type": "string"
                          },
                          "error": {
                            "type": "string"
                          }
                        }
                      }
                    }
                  }
                }
              }
            }
          }
        },
        "parameters": [
          {
            "name": "session_id",
            "in": "query",
            "schema": {
              "type": "string"
            },
            "description": "Report each tool's enabled state for this session."
          }
        ]
      }
    },
    "/tools/reload": {
      "post": {
        "tags": [
          "tools"
        ],
        "summary": "Validate and register tools from disk. Previously loaded tools keep working if a new one fails.",
        "responses": {
          "200": {
            "description": "Reload result.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "added": {
                      "type": "array",
                      "items": {
                        "type": "string"
                      }
                    },
                    "removed": {
                      "type": "array",
                      "items": {
                        "type": "string"
                      }
                    },
                    "failed": {
                      "type": "array",
                      "items": {
                        "type": "object",
                        "properties": {
                          "name": {
                            "type": "string"
                          },
                          "error": {
                            "type": "string"
                          }
                        }
                      }
                    },
                    "commit": {
                      "type": "string"
                    }
                  }
                }
              }
            }
          }
        }
      }
    },
    "/skills": {
      "get": {
        "tags": [
          "skills"
        ],
        "summary": "List skills with their descriptions.",
        "responses": {
          "200": {
            "description": "Skills.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "$ref": "#/components/schemas/Skill"
                  }
                }
              }
            }
          }
        }
      }
    },
    "/uploads": {
      "post": {
        "tags": [
          "system"
        ],
        "summary": "Upload a file into the workspace.",
        "requestBody": {
          "required": true,
          "content": {
            "multipart/form-data": {
              "schema": {
                "type": "object",
                "properties": {
                  "file": {
                    "type": "string",
                    "format": "binary"
                  }
                }
              }
            }
          }
        },
        "responses": {
          "201": {
            "description": "Stored.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "id": {
                      "type": "string"
                    },
                    "path": {
                      "type": "string"
                    },
                    "content_type": {
                      "type": "string"
                    },
                    "bytes": {
                      "type": "integer"
                    }
                  }
                }
              }
            }
          },
          "413": {
            "description": "File exceeds 100 MB."
          }
        },
        "description": "Maximum 100 MB per file. Uploads belong to the workspace and are deleted with the session that referenced them."
      }
    },
    "/models": {
      "get": {
        "tags": [
          "system"
        ],
        "summary": "Models that support tool calling, with context window and price. Cached from OpenRouter, refreshed daily.",
        "responses": {
          "200": {
            "description": "Models.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": {
                      "id": {
                        "type": "string"
                      },
                      "name": {
                        "type": "string"
                      },
                      "context_length": {
                        "type": "integer"
                      },
                      "price_prompt": {
                        "type": "string"
                      },
                      "price_completion": {
                        "type": "string"
                      },
                      "explicit_cache_control": {
                        "type": "boolean"
                      }
                    }
                  }
                }
              }
            }
          }
        }
      }
    },
    "/status": {
      "get": {
        "tags": [
          "system"
        ],
        "summary": "Credit, usage, and scheduler health.",
        "responses": {
          "200": {
            "description": "Status.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "credit_remaining": {
                      "type": "number"
                    },
                    "usage_today": {
                      "type": "number"
                    },
                    "breaker": {
                      "type": "string",
                      "enum": [
                        "closed",
                        "open"
                      ]
                    },
                    "breaker_reason": {
                      "type": "string"
                    },
                    "jobs_active": {
                      "type": "integer"
                    },
                    "breaker_probe_at": {
                      "type": "string",
                      "format": "date-time",
                      "nullable": true,
                      "description": "When the next held job will be retried to test whether the breaker can close."
                    }
                  }
                }
              }
            }
          }
        }
      }
    },
    "/sessions/{id}/fork": {
      "parameters": [
        {
          "$ref": "#/components/parameters/Id"
        }
      ],
      "post": {
        "tags": [
          "sessions"
        ],
        "summary": "Continue this conversation in a new session.",
        "requestBody": {
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "properties": {
                  "archive": {
                    "type": "boolean",
                    "default": false,
                    "description": "Archive the predecessor and move its jobs to the successor. False leaves both live, which is a fork."
                  },
                  "model": {
                    "type": "string",
                    "description": "Omitted, the predecessor's is kept."
                  },
                  "enabled_tools": {
                    "type": "array",
                    "items": {
                      "type": "string"
                    },
                    "description": "Tool names for the successor. Three states: omit it to keep the predecessor's, send null for every one, send a list for exactly those."
                  },
                  "enabled_skills": {
                    "type": "array",
                    "items": {
                      "type": "string"
                    },
                    "description": "Skill names for the successor. Three states: omit it to keep the predecessor's, send null for every one, send a list for exactly those."
                  }
                }
              }
            }
          }
        },
        "responses": {
          "201": {
            "description": "The successor session.",
            "content": {
              "application/json": {
                "schema": {
                  "$ref": "#/components/schemas/Session"
                }
              }
            }
          },
          "404": {
            "$ref": "#/components/responses/NotFound"
          }
        },
        "description": "Rotation, fork, resume, and reconfiguration are one operation. The successor is seeded with the predecessor's summary and the most recent complete turns that fit the carry-over budget, and starts under the configuration given here, defaulting to the predecessor's. Both entries name what changed."
      }
    },
    "/jobs/{id}/runs": {
      "get": {
        "tags": [
          "jobs"
        ],
        "summary": "A job's run log: every wake, what came of it, and what was said. Newest first.",
        "parameters": [
          {
            "name": "id",
            "in": "path",
            "required": true,
            "schema": {
              "type": "string"
            }
          }
        ],
        "responses": {
          "200": {
            "description": "The job's runs.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": {
                      "id": {
                        "type": "string"
                      },
                      "job_id": {
                        "type": "string"
                      },
                      "session_id": {
                        "type": "string"
                      },
                      "at": {
                        "type": "string",
                        "format": "date-time",
                        "description": "When the wake ran."
                      },
                      "due_at": {
                        "type": "string",
                        "format": "date-time",
                        "description": "When it was scheduled for. Differs from at when the host slept or the session was busy."
                      },
                      "outcome": {
                        "type": "string",
                        "enum": [
                          "fired",
                          "skipped",
                          "failed"
                        ]
                      },
                      "message": {
                        "type": "string",
                        "description": "What the agent said, why the check said no, or what went wrong."
                      }
                    }
                  }
                }
              }
            }
          }
        }
      }
    }
  },
  "components": {
    "parameters": {
      "Id": {
        "name": "id",
        "in": "path",
        "required": true,
        "schema": {
          "type": "string"
        }
      }
    },
    "responses": {
      "NotFound": {
        "description": "No such resource."
      }
    },
    "schemas": {
      "Session": {
        "type": "object",
        "properties": {
          "id": {
            "type": "string",
            "description": "ULID."
          },
          "title": {
            "type": "string"
          },
          "model": {
            "type": "string"
          },
          "status": {
            "type": "string",
            "enum": [
              "active",
              "archived"
            ]
          },
          "enabled_tools": {
            "type": "array",
            "items": {
              "type": "string"
            },
            "nullable": true,
            "description": "Tools callable in this session. Null means all. A new session inherits the most recently used set."
          },
          "enabled_skills": {
            "type": "array",
            "items": {
              "type": "string"
            },
            "nullable": true,
            "description": "Skills readable in this session. Null means all. A new session inherits the most recently used set."
          },
          "unread": {
            "type": "integer"
          },
          "job_count": {
            "type": "integer"
          },
          "entry_count": {
            "type": "integer"
          },
          "context_used": {
            "type": "integer",
            "description": "Projected tokens against the model's window."
          },
          "cost": {
            "type": "number"
          },
          "cache_hit_rate": {
            "type": "number"
          },
          "created_at": {
            "type": "string",
            "format": "date-time"
          },
          "last_active_at": {
            "type": "string",
            "format": "date-time"
          },
          "summary": {
            "type": "string",
            "nullable": true,
            "description": "Rolling summary of this session, updated incrementally. Not part of this session's prompt; used to seed a successor."
          },
          "summary_updated_at": {
            "type": "string",
            "format": "date-time",
            "nullable": true
          },
          "continued_from": {
            "type": "string",
            "nullable": true,
            "description": "The session this one was seeded from, by rotation, fork, or resume."
          },
          "compact_at_tokens": {
            "type": "integer",
            "description": "Projected size at which this session compacts in place. Chosen for cost, not for the model's context limit."
          },
          "carry_over_tokens": {
            "type": "integer",
            "description": "Token budget for complete recent turns copied into a successor alongside the summary. Default 5000."
          },
          "forked_from": {
            "type": "string",
            "nullable": true,
            "description": "The session this one was copied from, if any. A fork does not supersede its origin, so there is no forward pointer."
          }
        }
      },
      "Entry": {
        "type": "object",
        "description": "A transcript entry. Entries of type event are shown to the user and never sent to the model.",
        "properties": {
          "seq": {
            "type": "integer"
          },
          "type": {
            "type": "string",
            "enum": [
              "message",
              "event"
            ]
          },
          "role": {
            "type": "string",
            "enum": [
              "user",
              "assistant",
              "tool"
            ]
          },
          "text": {
            "type": "string"
          },
          "tool_call": {
            "type": "object",
            "nullable": true,
            "properties": {
              "name": {
                "type": "string"
              },
              "arguments": {
                "type": "object"
              },
              "result": {
                "type": "object"
              }
            }
          },
          "job_id": {
            "type": "string",
            "nullable": true,
            "description": "Set when this entry was produced by a job."
          },
          "status": {
            "type": "string",
            "nullable": true,
            "enum": [
              "fired",
              "not_fired"
            ],
            "description": "Tick outcome. Consecutive entries sharing job_id and status collapse in the projection. For a check tick this reflects whether the check matched; for a model tick, whether notify was called."
          },
          "event_kind": {
            "type": "string",
            "nullable": true,
            "enum": [
              "prompt",
              "availability_change",
              "tool_added",
              "job_check",
              "summary",
              "rotation",
              "carried_over",
              "memory_write",
              "model_change",
              "breaker"
            ]
          },
          "usage": {
            "type": "object",
            "nullable": true,
            "properties": {
              "prompt_tokens": {
                "type": "integer"
              },
              "completion_tokens": {
                "type": "integer"
              },
              "cached_tokens": {
                "type": "integer"
              },
              "cost": {
                "type": "number"
              },
              "tokens_per_second": {
                "type": "number"
              }
            }
          },
          "created_at": {
            "type": "string",
            "format": "date-time"
          },
          "sections": {
            "type": "array",
            "nullable": true,
            "description": "Present on a prompt entry: the system prompt as sent, split into readable sections.",
            "items": {
              "type": "object",
              "properties": {
                "name": {
                  "type": "string",
                  "enum": [
                    "persona",
                    "memory",
                    "skills_index",
                    "tool_schemas",
                    "platform"
                  ]
                },
                "text": {
                  "type": "string"
                },
                "tokens": {
                  "type": "integer"
                },
                "editable": {
                  "type": "boolean",
                  "description": "True only for memory."
                }
              }
            }
          }
        }
      },
      "JobSpec": {
        "type": "object",
        "required": [
          "session_id",
          "schedule",
          "prompt"
        ],
        "properties": {
          "session_id": {
            "type": "string"
          },
          "schedule": {
            "type": "string",
            "description": "An interval (2m), a cron expression (0 9 * * *), or an RFC 3339 instant."
          },
          "check": {
            "type": "string",
            "nullable": true,
            "description": "A shell command. Exit status zero means the condition is met. Omit for a job whose condition requires judgement, in which case the model runs every tick and signals by calling notify."
          },
          "prompt": {
            "type": "string",
            "description": "What the agent is asked when the job runs. "
          },
          "expires_at": {
            "type": "string",
            "format": "date-time",
            "description": "Defaults to 24 hours after creation."
          },
          "on_condition_met": {
            "type": "string",
            "enum": [
              "delete",
              "continue"
            ],
            "default": "delete"
          }
        }
      },
      "Job": {
        "type": "object",
        "properties": {
          "session_id": {
            "type": "string"
          },
          "schedule": {
            "type": "string",
            "description": "An interval (2m), a cron expression (0 9 * * *), or an RFC 3339 instant."
          },
          "check": {
            "type": "string",
            "nullable": true,
            "description": "A shell command. Exit status zero means the condition is met. Omit for a job whose condition requires judgement, in which case the model runs every tick and signals by calling notify."
          },
          "prompt": {
            "type": "string",
            "description": "What the agent is asked when the job runs. "
          },
          "expires_at": {
            "type": "string",
            "format": "date-time",
            "description": "Defaults to 24 hours after creation."
          },
          "on_condition_met": {
            "type": "string",
            "enum": [
              "delete",
              "continue"
            ],
            "default": "delete"
          },
          "id": {
            "type": "string"
          },
          "run_count": {
            "type": "integer"
          },
          "next_run_at": {
            "type": "string",
            "format": "date-time"
          },
          "last_status": {
            "type": "string",
            "nullable": true,
            "enum": [
              "fired",
              "not_fired"
            ]
          },
          "created_at": {
            "type": "string",
            "format": "date-time"
          }
        }
      },
      "DeadLetter": {
        "type": "object",
        "properties": {
          "id": {
            "type": "string"
          },
          "job_id": {
            "type": "string"
          },
          "job_spec": {
            "$ref": "#/components/schemas/JobSpec"
          },
          "reason": {
            "type": "string",
            "enum": [
              "expired",
              "error",
              "unavailable"
            ]
          },
          "detail": {
            "type": "string",
            "nullable": true
          },
          "run_count": {
            "type": "integer"
          },
          "session_id": {
            "type": "string"
          },
          "status": {
            "type": "string",
            "enum": [
              "open",
              "closed"
            ]
          },
          "created_at": {
            "type": "string",
            "format": "date-time"
          }
        }
      },
      "MemoryItem": {
        "type": "object",
        "properties": {
          "id": {
            "type": "string"
          },
          "text": {
            "type": "string"
          },
          "source_session": {
            "type": "string"
          },
          "created_at": {
            "type": "string",
            "format": "date-time"
          }
        }
      },
      "Tool": {
        "type": "object",
        "properties": {
          "name": {
            "type": "string"
          },
          "description": {
            "type": "string"
          },
          "db_prefix": {
            "type": "string"
          },
          "timeout_seconds": {
            "type": "integer"
          },
          "parameters": {
            "type": "object",
            "description": "JSON Schema exposed to the model."
          },
          "has_panel": {
            "type": "boolean"
          },
          "loaded_at": {
            "type": "string",
            "format": "date-time"
          },
          "enabled": {
            "type": "boolean",
            "description": "Whether this tool is callable in the requested session."
          }
        }
      },
      "Skill": {
        "type": "object",
        "properties": {
          "name": {
            "type": "string"
          },
          "description": {
            "type": "string",
            "description": "One line. Only this and the name reach the system prompt."
          },
          "bytes": {
            "type": "integer"
          }
        }
      }
    }
  }
};
