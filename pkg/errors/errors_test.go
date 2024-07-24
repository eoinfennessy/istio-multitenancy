package errors

import (
	"errors"
	"testing"
)

func TestIsUnreconcilableError(t *testing.T) {
	type args struct {
		err error
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "nil error",
			args: args{err: nil},
			want: false,
		},
		{
			name: "generic error",
			args: args{err: errors.New("generic error")},
			want: false,
		},
		{
			name: "unreconcilable error",
			args: args{err: NewUnreconcilableError("unreconcilable error")},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsUnreconcilableError(tt.args.err); got != tt.want {
				t.Errorf("IsUnreconcilableError() = %v, want %v", got, tt.want)
			}
		})
	}
}
