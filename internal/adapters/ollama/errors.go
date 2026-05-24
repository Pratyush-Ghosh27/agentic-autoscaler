/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package ollama

import "errors"

// ErrModelNotFound is returned when Ollama responds with 404, indicating
// the requested model has not been pulled. Per design §9 the caller logs
// a warning suggesting `ollama pull <model>`.
var ErrModelNotFound = errors.New("ollama: model not found (run `ollama pull <model>`)")

// ErrEmptyResponse is returned when Ollama returns a 200 with no choices
// or an empty content string. Per design §9 the caller emits no event.
var ErrEmptyResponse = errors.New("ollama: empty response")
