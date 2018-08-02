package v1

import (
	"net/url"
)

// UnmarshalJSON unmarshals a URL, ensuring that it is valid.
func (u *URL) UnmarshalJSON(data []byte) error {
	_, err := url.Parse(string(data))
	if err != nil {
		return err
	}

	*u = URL(string(data))

	return nil
}
