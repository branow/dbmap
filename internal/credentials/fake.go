package credentials

// Fake is an in-memory Store. It ships in the package rather than in a test
// file so every command's tests can run without a keychain, a file or an
// environment variable.
type Fake struct {
	// Values holds the stored secrets by key, for assertions.
	Values map[string]string
	// Err, when set, is returned by every operation, which is how a test
	// drives the failure branches.
	Err error
}

// NewFake returns an empty in-memory store.
func NewFake() *Fake { return &Fake{Values: map[string]string{}} }

func (f *Fake) Get(key string) (Secret, error) {
	if f.Err != nil {
		return Secret{}, f.Err
	}
	value, ok := f.Values[key]
	if !ok {
		return Secret{}, &NotFoundError{Key: key}
	}
	return NewSecret(value), nil
}

func (f *Fake) Set(key string, secret Secret) error {
	if f.Err != nil {
		return f.Err
	}
	if f.Values == nil {
		f.Values = map[string]string{}
	}
	f.Values[key] = secret.Reveal()
	return nil
}

func (f *Fake) Delete(key string) error {
	if f.Err != nil {
		return f.Err
	}
	delete(f.Values, key)
	return nil
}
