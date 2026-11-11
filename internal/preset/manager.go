package preset

import (
	"errors"
	"fmt"

	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
)

var ErrPersistence = errors.New("preset persistence failed")

type Manager struct {
	repository *Repository
	settings   *settings.Settings
	library    *Library
}

func NewManager(repository *Repository, value *settings.Settings) (*Manager, error) {
	if repository == nil {
		return nil, errors.New("preset repository is required")
	}
	if value == nil {
		return nil, errors.New("settings value is required")
	}
	library, err := repository.Load(*value)
	if err != nil {
		return nil, err
	}
	return &Manager{repository: repository, settings: value, library: library}, nil
}

func (m *Manager) Library() *Library {
	return m.library
}

func (m *Manager) Create(name string, width, height int, mode ResizeMode) (Preset, error) {
	created, err := m.library.Create(name, width, height, mode)
	if err != nil {
		return Preset{}, err
	}
	if err := m.library.Select(created.ID); err != nil {
		return Preset{}, err
	}
	return created, m.save()
}

func (m *Manager) Update(id, name string, width, height int, mode ResizeMode) error {
	if err := m.library.Update(id, name, width, height, mode); err != nil {
		return err
	}
	return m.save()
}

func (m *Manager) Duplicate(id string) (Preset, error) {
	duplicated, err := m.library.Duplicate(id)
	if err != nil {
		return Preset{}, err
	}
	if err := m.library.Select(duplicated.ID); err != nil {
		return Preset{}, err
	}
	return duplicated, m.save()
}

func (m *Manager) Delete(id string) error {
	if err := m.library.Delete(id); err != nil {
		return err
	}
	return m.save()
}

func (m *Manager) Select(id string) error {
	if err := m.library.Select(id); err != nil {
		return err
	}
	return m.save()
}

func (m *Manager) save() error {
	if err := m.repository.Save(m.settings, m.library); err != nil {
		return fmt.Errorf("%w: %w", ErrPersistence, err)
	}
	return nil
}
