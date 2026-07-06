// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package server

type InitMessage struct {
	Arguments string `json:"Arguments,omitempty"`
	AuthToken string `json:"AuthToken,omitempty"`
}
