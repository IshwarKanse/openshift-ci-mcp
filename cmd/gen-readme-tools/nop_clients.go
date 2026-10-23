package main

import (
	"context"

	"github.com/openshift-eng/openshift-ci-mcp/pkg/client"
)

type nopSippy struct{}

func (nopSippy) Get(_ context.Context, _ string, _ map[string]string) ([]byte, error) {
	return nil, nil
}

type nopRC struct{}

func (nopRC) Get(_ context.Context, _ string, _ map[string]string) ([]byte, error) {
	return nil, nil
}

func (nopRC) GetForArch(_ context.Context, _, _ string, _ map[string]string) ([]byte, error) {
	return nil, nil
}

type nopSearch struct{}

func (nopSearch) Search(_ context.Context, _ string, _ map[string]string) ([]byte, error) {
	return nil, nil
}

func (nopSearch) Get(_ context.Context, _ string, _ map[string]string) ([]byte, error) {
	return nil, nil
}

type nopProw struct{}

func (nopProw) Decks() []client.Deck { return nil }

func (nopProw) JobHistory(_ context.Context, _ client.Deck, _, _, _ string) (*client.JobHistoryPage, error) {
	return nil, nil
}

func (nopProw) List(_ context.Context, _ client.Deck, _ string) (*client.Listing, error) {
	return nil, nil
}

func (nopProw) Open(_ context.Context, _ client.Deck, _ string) (*client.Object, error) {
	return nil, nil
}
