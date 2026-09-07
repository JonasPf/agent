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
        "summary": "Create a session.",
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
                    "description": "Accepted only before the session first turn."
                  },
                  "continued_from": {
                    "type": "string",
                    "description": "Seed the new session with that session's summary. Used for fork and resume; rotation does the same automatically."
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
        }
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
        "description": "An archived session is returned as it was. Follow continued_by to reach the live session of its chain."
      },
      "patch": {
        "tags": [
          "sessions"
        ],
        "summary": "Update title, status, or the summary; also model, tools, and skills before the first turn.",
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
                    "description": "Accepted only before the session first turn. Null means all.",
                    "items": {
                      "type": "string"
                    }
                  },
                  "enabled_skills": {
                    "type": "array",
                    "description": "Accepted only before the session first turn. Null means all.",
                    "items": {
                      "type": "string"
                    }
                  },
                  "summary": {
                    "type": "string",
                    "description": "Edit the rolling summary."
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
            "description": "Refused: the session has taken a turn, so its model, tools, and skills are fixed. Rotate to continue under a new configuration.",
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
        "description": "Model, tools, and skills are one configuration, and it settles at the session's first turn: the prompt entry is written then, from exactly that configuration. Before the first turn this endpoint edits it; afterwards it is refused with 409 and POST /sessions/{id}/rotate continues the conversation in a new session under the new configuration."
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
    "/sessions/{id}/files": {
      "parameters": [
        {
          "$ref": "#/components/parameters/Id"
        }
      ],
      "get": {
        "tags": [
          "sessions"
        ],
        "summary": "List the files in the session's working directory.",
        "responses": {
          "200": {
            "description": "Files, newest first.",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "$ref": "#/components/schemas/SessionFile"
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
        "summary": "Upload a file into the session's working directory.",
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
                  "$ref": "#/components/schemas/SessionFile"
                }
              }
            }
          },
          "413": {
            "description": "File exceeds 100 MB."
          }
        },
        "description": "Maximum 100 MB per file. The file lands in the session's own working directory, where its tools run, and is deleted with the session. An upload writes no transcript entry."
      }
    },
    "/sessions/{id}/files/{path}": {
      "parameters": [
        {
          "$ref": "#/components/parameters/Id"
        },
        {
          "name": "path",
          "in": "path",
          "required": true,
          "schema": {
            "type": "string"
          },
          "description": "Path relative to the session's working directory. A path that would leave it is refused."
        }
      ],
      "get": {
        "tags": [
          "sessions"
        ],
        "summary": "Download one file.",
        "responses": {
          "200": {
            "description": "The file.",
            "content": {
              "application/octet-stream": {
                "schema": {
                  "type": "string",
                  "format": "binary"
                }
              }
            }
          },
          "404": {
            "description": "No such file."
          }
        }
      },
      "delete": {
        "tags": [
          "sessions"
        ],
        "summary": "Delete one file.",
        "responses": {
          "204": {
            "description": "Deleted."
          },
          "404": {
            "description": "No such file."
          }
        }
      }
    },
    "/sessions/{id}/export": {
      "parameters": [
        {
          "$ref": "#/components/parameters/Id"
        }
      ],
      "get": {
        "tags": [
          "sessions"
        ],
        "summary": "Export the session as a zip.",
        "responses": {
          "200": {
            "description": "An archive holding meta.json, transcript.jsonl, jobs.json, job_runs.json, and the working directory under files/.",
            "content": {
              "application/zip": {
                "schema": {
                  "type": "string",
                  "format": "binary"
                }
              }
            }
          }
        },
        "description": "Memory is not included: it is durable across every conversation rather than owned by one."
      }
    },
    "/sessions/import": {
      "post": {
        "tags": [
          "sessions"
        ],
        "summary": "Restore a session from an archive.",
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
            },
            "application/zip": {
              "schema": {
                "type": "string",
                "format": "binary"
              }
            }
          }
        },
        "responses": {
          "201": {
            "description": "Restored.",
            "content": {
              "application/json": {
                "schema": {
                  "$ref": "#/components/schemas/Session"
                }
              }
            }
          },
          "400": {
            "description": "Not a session archive."
          },
          "409": {
            "description": "A session with that identifier is already here."
          }
        },
        "description": "The session keeps the identifier the archive carries, so a restored conversation is the one it was. An identifier already present is a conflict, not a merge."
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
    "/models": {
      "get": {
        "tags": [
          "system"
        ],
        "summary": "Models that support tool calling, with context window and price. Cached from OpenRouter, refreshed daily.",
        "parameters": [
          {
            "name": "q",
            "in": "query",
            "required": false,
            "description": "Free-text search. Split on whitespace; every term must appear, case-insensitively, in the model id or name. Omitted or blank returns the whole catalogue. Catalogue order is preserved.",
            "schema": {
              "type": "string"
            }
          }
        ],
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
      "SessionFile": {
        "type": "object",
        "properties": {
          "path": {
            "type": "string",
            "description": "Path relative to the session's working directory."
          },
          "bytes": {
            "type": "integer"
          },
          "modified_at": {
            "type": "string",
            "format": "date-time"
          }
        }
      },
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
          "disk_bytes": {
            "type": "integer",
            "description": "What the session occupies on disk: its transcript and metadata plus its working directory."
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
          "rotate_at_tokens": {
            "type": "integer",
            "description": "Projected size at which this session rotates into a successor. Chosen for cost, not for the model's context limit."
          },
          "carry_over_tokens": {
            "type": "integer",
            "description": "Token budget for complete recent turns copied into a successor alongside the summary. Default 5000."
          },
          "continued_by": {
            "type": "string",
            "nullable": true,
            "description": "The session that succeeded this one. Following continued_by to its end gives the live session of this chain."
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
              "event",
              "prompt"
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
            "description": "Tick outcome. Consecutive job_check entries sharing job_id and status collapse in the projection."
          },
          "event_kind": {
            "type": "string",
            "nullable": true,
            "description": "Set only when type is event. An event records something no other entry and no other screen records.",
            "enum": [
              "job_check",
              "job_error",
              "error",
              "rotation",
              "carried_over"
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
            "description": "When the job wakes: an interval (30m), a cron expression (0 9 * * *), or a single RFC 3339 instant. An instant wakes once."
          },
          "check": {
            "type": "string",
            "nullable": true,
            "description": "A shell command run before the prompt. Exit status zero means act on this wake; anything else means skip it. Omit to act every time."
          },
          "prompt": {
            "type": "string",
            "description": "What the agent is asked when the job acts. The whole turn it produces is written to the transcript, attributed to the job."
          },
          "after_acting": {
            "type": "string",
            "enum": [
              "stop",
              "continue"
            ],
            "default": "continue",
            "description": "What becomes of the job once it has acted. Named for that moment rather than for the schedule: a field called repeat was read as a question about cadence."
          },
          "status": {
            "type": "string",
            "enum": [
              "scheduled",
              "done"
            ],
            "description": "scheduled until the job has nothing left to do, then done. A done job is kept, with its log."
          },
          "estimate": {
            "type": "object",
            "description": "What this job will cost. Computed on the way out, never stored: the conversation it prices grows with every turn.",
            "properties": {
              "wakes_per_day": {
                "type": "number",
                "description": "Zero for a schedule that fires once."
              },
              "tokens_per_turn": {
                "type": "integer",
                "description": "What one wake that acts re-sends: the system prompt plus the conversation. Prompt side only."
              },
              "tokens_per_day": {
                "type": "integer",
                "description": "Zero for a gated job and for one that wakes once."
              },
              "cost_per_day": {
                "type": "number"
              },
              "cost_per_month": {
                "type": "number"
              },
              "priced": {
                "type": "boolean",
                "description": "False when the model's price is unknown. The token figures still hold."
              },
              "gated": {
                "type": "boolean",
                "description": "A check decides each wake, so wakes cost nothing until it passes."
              },
              "note": {
                "type": "string",
                "description": "What the estimate is based on."
              }
            }
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
            "description": "When the job wakes: an interval (30m), a cron expression (0 9 * * *), or a single RFC 3339 instant. An instant wakes once."
          },
          "check": {
            "type": "string",
            "nullable": true,
            "description": "A shell command run before the prompt. Exit status zero means act on this wake; anything else means skip it. Omit to act every time."
          },
          "prompt": {
            "type": "string",
            "description": "What the agent is asked when the job acts. The whole turn it produces is written to the transcript, attributed to the job."
          },
          "after_acting": {
            "type": "string",
            "enum": [
              "stop",
              "continue"
            ],
            "default": "continue",
            "description": "What becomes of the job once it has acted. Named for that moment rather than for the schedule: a field called repeat was read as a question about cadence."
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
          },
          "status": {
            "type": "string",
            "enum": [
              "scheduled",
              "done"
            ],
            "description": "scheduled until the job has nothing left to do, then done. A done job is kept, with its log."
          },
          "estimate": {
            "type": "object",
            "description": "What this job will cost. Computed on the way out, never stored: the conversation it prices grows with every turn.",
            "properties": {
              "wakes_per_day": {
                "type": "number",
                "description": "Zero for a schedule that fires once."
              },
              "tokens_per_turn": {
                "type": "integer",
                "description": "What one wake that acts re-sends: the system prompt plus the conversation. Prompt side only."
              },
              "tokens_per_day": {
                "type": "integer",
                "description": "Zero for a gated job and for one that wakes once."
              },
              "cost_per_day": {
                "type": "number"
              },
              "cost_per_month": {
                "type": "number"
              },
              "priced": {
                "type": "boolean",
                "description": "False when the model's price is unknown. The token figures still hold."
              },
              "gated": {
                "type": "boolean",
                "description": "A check decides each wake, so wakes cost nothing until it passes."
              },
              "note": {
                "type": "string",
                "description": "What the estimate is based on."
              }
            }
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
}
;
