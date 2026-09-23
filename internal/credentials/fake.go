package credentials

// Fake is an in-memory Store. It ships in the package, not in a test file, so
// every command's tests can run with no keychain.
type Fake struct {
	Values map[string]string
	// Err, when set, is returned by every operation.
	Err error
}

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
