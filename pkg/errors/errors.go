/*
Copyright Red Hat

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package errors

import "errors"

type UnreconcilableError struct {
	Err error
}

func (r *UnreconcilableError) Error() string {
	return r.Err.Error()
}

func NewUnreconcilableError(text string) *UnreconcilableError {
	return &UnreconcilableError{Err: errors.New(text)}
}

func IsUnreconcilableError(err error) bool {
	var unreconcilableError *UnreconcilableError
	ok := errors.As(err, &unreconcilableError)
	return ok
}

func IgnoreUnreconcilableError(err error) error {
	if IsUnreconcilableError(err) {
		return nil
	}
	return err
}
