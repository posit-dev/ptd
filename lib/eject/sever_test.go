package eject

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/posit-dev/ptd/lib/types"
	"github.com/posit-dev/ptd/lib/types/typestest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type fakeSecretStore struct {
	secrets map[string]string
}

func (f *fakeSecretStore) SecretExists(_ context.Context, _ types.Credentials, secretName string) bool {
	_, ok := f.secrets[secretName]
	return ok
}

func (f *fakeSecretStore) GetSecretValue(_ context.Context, _ types.Credentials, secretName string) (string, error) {
	return f.secrets[secretName], nil
}

func (f *fakeSecretStore) PutSecretValue(_ context.Context, _ types.Credentials, secretName string, secretString string) error {
	f.secrets[secretName] = secretString
	return nil
}

func (f *fakeSecretStore) CreateSecret(_ context.Context, _ types.Credentials, _ string, _ string) error {
	return nil
}

func (f *fakeSecretStore) CreateSecretIfNotExists(_ context.Context, _ types.Credentials, _ string, _ any) error {
	return nil
}

func (f *fakeSecretStore) EnsureWorkloadSecret(_ context.Context, _ types.Credentials, _ string, _ any) error {
	return nil
}

func controlRoomTarget(name string, store types.SecretStore) *typestest.MockTarget {
	mt := &typestest.MockTarget{}
	mt.On("Name").Return(name)
	mt.On("Credentials", mock.Anything).Return(typestest.DefaultCredentials(), nil)
	mt.On("SecretStore").Return(store)
	return mt
}

func TestRemoveWorkloadMimirPassword_MultipleEntries(t *testing.T) {
	secretName := "ctrl.mimir-auth.posit.team"
	initial := map[string]string{"workload-a": "pass-a", "workload-b": "pass-b"}
	data, err := json.Marshal(initial)
	require.NoError(t, err)

	store := &fakeSecretStore{secrets: map[string]string{secretName: string(data)}}
	target := controlRoomTarget("ctrl", store)

	err = RemoveWorkloadMimirPassword(context.Background(), target, "workload-a")
	require.NoError(t, err)

	var result map[string]string
	require.NoError(t, json.Unmarshal([]byte(store.secrets[secretName]), &result))
	assert.Equal(t, map[string]string{"workload-b": "pass-b"}, result)
}

func TestRemoveWorkloadMimirPassword_OnlyEntry(t *testing.T) {
	secretName := "ctrl.mimir-auth.posit.team"
	initial := map[string]string{"workload-a": "pass-a"}
	data, err := json.Marshal(initial)
	require.NoError(t, err)

	store := &fakeSecretStore{secrets: map[string]string{secretName: string(data)}}
	target := controlRoomTarget("ctrl", store)

	err = RemoveWorkloadMimirPassword(context.Background(), target, "workload-a")
	require.NoError(t, err)

	var result map[string]string
	require.NoError(t, json.Unmarshal([]byte(store.secrets[secretName]), &result))
	assert.Empty(t, result)
}

func TestRemoveWorkloadMimirPassword_SecretDoesNotExist(t *testing.T) {
	store := &fakeSecretStore{secrets: map[string]string{}}
	target := controlRoomTarget("ctrl", store)

	err := RemoveWorkloadMimirPassword(context.Background(), target, "workload-a")
	assert.NoError(t, err)
}

func TestRemoveWorkloadMimirPassword_WorkloadNotInMap(t *testing.T) {
	secretName := "ctrl.mimir-auth.posit.team"
	initial := map[string]string{"workload-b": "pass-b"}
	data, err := json.Marshal(initial)
	require.NoError(t, err)

	store := &fakeSecretStore{secrets: map[string]string{secretName: string(data)}}
	target := controlRoomTarget("ctrl", store)

	err = RemoveWorkloadMimirPassword(context.Background(), target, "workload-a")
	require.NoError(t, err)

	var result map[string]string
	require.NoError(t, json.Unmarshal([]byte(store.secrets[secretName]), &result))
	assert.Equal(t, map[string]string{"workload-b": "pass-b"}, result)
}
