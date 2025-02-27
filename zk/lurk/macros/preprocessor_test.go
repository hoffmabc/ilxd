// Copyright (c) 2024 The illium developers
// Use of this source code is governed by an MIT
// license that can be found in the LICENSE file.

package macros_test

import (
	"errors"
	"github.com/project-illium/ilxd/zk/lurk/macros"
	"github.com/stretchr/testify/assert"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreProcessValidParentheses(t *testing.T) {
	type testVector struct {
		input    string
		expected string
	}
	tests := []testVector{
		{"(+ x 3)", "(+ x 3)"},
		{"!(def x 3)", "(let ((x 3)))"},
		{"!(def x (car y))", "(let ((x (car y))))"},
		{"!(def x 3) t", "(let ((x 3)) t)"},
		{"!(def x (car y)) t", "(let ((x (car y))) t)"},
		{"!(defrec x 3)", "(letrec ((x 3)))"},
		{"!(defrec x (car y))", "(letrec ((x (car y))))"},
		{"!(defrec x 3) t", "(letrec ((x 3)) t)"},
		{"!(defrec x (car y)) t", "(letrec ((x (car y))) t)"},
		{"!(defun f (x) 3)", "(letrec ((f (lambda (x) 3))))"},
		{"!(defun f (x) (+ x 3))", "(letrec ((f (lambda (x) (+ x 3)))))"},
		{"!(defun f (x) (+ x 3)) t", "(letrec ((f (lambda (x) (+ x 3)))) t)"},
		{"!(assert t)", "(if (eq t nil) nil)"},
		{"!(assert (+ x 5)) nil", "(if (eq (+ x 5) nil) nil nil)"},
		{"!(assert t) nil", "(if (eq t nil) nil nil)"},
		{"!(assert-eq x 3)", "(if (eq (eq x 3 ) nil) nil)"},
		{"!(assert-eq x 3) t", "(if (eq (eq x 3 ) nil) nil t)"},
		{"!(defun f (x) (!(assert t) 3))", "(letrec ((f (lambda (x) (if (eq t nil) nil 3)))))"},
		{"(lambda (script-params unlocking-params input-index private-params public-params) !(assert-eq (+ x 5) 4) !(def z 5) !(assert t) t)", "(lambda (script-params unlocking-params input-index private-params public-params) (if (eq (eq (+ x 5) 4) nil) nil (let ((z 5)) (if (eq t nil) nil t))))"},
		{"!(list 1 2 3 4)", "(cons 1 (cons 2 (cons 3 (cons 4 nil))))"},
		{"!(list 1 (car x) 3 4)", "(cons 1 (cons (car x) (cons 3 (cons 4 nil))))"},
		{"!(param nullifiers 0)", "(car (car (cdr public-params)))"},
		{"!(param nullifiers 1)", "(car (cdr (car (cdr public-params))))"},
		{"!(param sighash)", "(car public-params)"},
		{"!(param txo-root)", "(car (cdr (cdr public-params)))"},
		{"!(param fee)", "(car (cdr (cdr (cdr public-params))))"},
		{"!(param mint-id)", "(car (cdr (cdr (cdr (cdr public-params)))))"},
		{"!(param mint-amount)", "(car (cdr (cdr (cdr (cdr (cdr public-params))))))"},
		{"!(param locktime)", "(car (cdr (cdr (cdr (cdr (cdr (cdr (cdr public-params))))))))"},
		{"!(param locktime-precision)", "(car (cdr (cdr (cdr (cdr (cdr (cdr (cdr (cdr public-params)))))))))"},
		{"!(param priv-in 2)", "(car (cdr (cdr (car private-params))))"},
		{"!(param priv-out 3)", "(car (cdr (cdr (cdr (car (cdr private-params))))))"},
		{"!(param priv-out 4)", "(car (cdr (cdr (cdr (cdr (car (cdr private-params)))))))"},
		{"!(param priv-in 2 amount)", "(car (car (cdr (cdr (car private-params)))))"},
		{"!(param priv-in 2 asset-id)", "(car (cdr (car (cdr (cdr (car private-params))))))"},
		{"!(param priv-in 2 salt)", "(car (cdr (cdr (car (cdr (cdr (car private-params)))))))"},
		{"!(param priv-in 2 state)", "(car (cdr (cdr (cdr (car (cdr (cdr (car private-params))))))))"},
		{"!(param priv-in 2 commitment-index)", "(car (cdr (cdr (cdr (cdr (car (cdr (cdr (car private-params)))))))))"},
		{"!(param priv-in 2 inclusion-proof)", "(car (cdr (cdr (cdr (cdr (cdr (car (cdr (cdr (car private-params))))))))))"},
		{"!(param priv-in 2 locking-params)", "(car (cdr (cdr (cdr (cdr (cdr (cdr (cdr (car (cdr (cdr (car private-params))))))))))))"},
		{"!(param priv-in 2 unlocking-params)", "(car (cdr (cdr (cdr (cdr (cdr (cdr (cdr (cdr (car (cdr (cdr (car private-params)))))))))))))"},
		{"!(param priv-out 3 script-hash)", "(car (car (cdr (cdr (cdr (car (cdr private-params)))))))"},
		{"!(param priv-out 3 amount)", "(car (cdr (car (cdr (cdr (cdr (car (cdr private-params))))))))"},
		{"!(param priv-out 3 asset-id)", "(car (cdr (cdr (car (cdr (cdr (cdr (car (cdr private-params)))))))))"},
		{"!(param priv-out 3 salt)", "(car (cdr (cdr (cdr (car (cdr (cdr (cdr (car (cdr private-params))))))))))"},
		{"!(param priv-out 3 state)", "(car (cdr (cdr (cdr (cdr (car (cdr (cdr (cdr (car (cdr private-params)))))))))))"},
		{"!(param pub-out 4 commitment)", "(car (car (cdr (cdr (cdr (cdr (car (cdr (cdr (cdr (cdr (cdr (cdr (cdr public-params))))))))))))))"},
		{"!(param pub-out 4 ciphertext)", "(car (cdr (car (cdr (cdr (cdr (cdr (car (cdr (cdr (cdr (cdr (cdr (cdr (cdr public-params)))))))))))))))"},
	}

	mp, err := macros.NewMacroPreprocessor()
	assert.NoError(t, err)
	for i, test := range tests {
		lurkProgram, err := mp.Preprocess(test.input)
		lurkProgram = strings.ReplaceAll(lurkProgram, "\n", "")
		lurkProgram = strings.ReplaceAll(lurkProgram, "\t", "")
		assert.NoError(t, err)
		assert.Truef(t, macros.IsValidLurk(lurkProgram), "Test %d should be valid", i)
		assert.Equalf(t, test.expected, lurkProgram, "Test %d not as expected", i)
	}
}

func TestMacroImports(t *testing.T) {
	tempDir := path.Join(os.TempDir(), "marco_test")
	defer os.Remove(tempDir)

	type module struct {
		path string
		file string
	}
	type testVector struct {
		input    string
		modules  []module
		expected string
	}

	mod1 := `!(module math (
			!(defun plus-two (x) (+ x 2))
			!(defun plus-three (x) (+ x 3))
			!(def some-const 1234)
		))

		!(module time (
			!(assert (<= !(param locktime-precision) 30))
		))
		`

	tests := []testVector{
		{
			input: `!(defun my-func (y) (
				!(import math)
				(plus-two 10)
			))`,
			modules:  []module{{path: filepath.Join(tempDir, "mod.lurk"), file: mod1}},
			expected: "(letrec ((my-func (lambda (y) (letrec ((plus-two (lambda (x) (+ x 2))))(letrec ((plus-three (lambda (x) (+ x 3))))(let ((some-const 1234))(plus-two 10))))))))",
		},
		{
			input: `!(defun my-func (y) (
				!(import time)
				(plus-two 10)
			))`,
			modules:  []module{{path: filepath.Join(tempDir, "mod.lurk"), file: mod1}},
			expected: "(letrec ((my-func (lambda (y) (if (eq (<= (car (cdr (cdr (cdr (cdr (cdr (cdr (cdr (cdr public-params))))))))) 30) nil) nil(plus-two 10))))))",
		},
		{
			input: `!(defun my-func (y) (
				!(import std/math)
				(plus-two 10)
			))`,
			modules:  []module{{path: filepath.Join(tempDir, "std", "mod.lurk"), file: mod1}},
			expected: "(letrec ((my-func (lambda (y) (letrec ((plus-two (lambda (x) (+ x 2))))(letrec ((plus-three (lambda (x) (+ x 3))))(let ((some-const 1234))(plus-two 10))))))))",
		},
		{
			input: `!(defun my-func (y) (
				!(import math/plus-two)
				(plus-two 10)
			))`,
			modules:  []module{{path: filepath.Join(tempDir, "mod.lurk"), file: mod1}},
			expected: "(letrec ((my-func (lambda (y) (letrec ((plus-two (lambda (x) (+ x 2))))(plus-two 10))))))",
		},
		{
			input: `!(defun my-func (y) (
				!(import math/some-const)
				(+ some-const 21)
			))`,
			modules:  []module{{path: filepath.Join(tempDir, "mod.lurk"), file: mod1}},
			expected: "(letrec ((my-func (lambda (y) (let ((some-const 1234))(+ some-const 21))))))",
		},
	}

	mp, err := macros.NewMacroPreprocessor(macros.DependencyDir(tempDir))
	assert.NoError(t, err)
	for i, test := range tests {
		for _, mod := range test.modules {
			err := os.MkdirAll(filepath.Dir(mod.path), 0755)
			assert.NoError(t, err)

			err = os.WriteFile(mod.path, []byte(mod.file), 0644)
			assert.NoError(t, err)
		}

		lurkProgram, err := mp.Preprocess(test.input)
		lurkProgram = strings.ReplaceAll(lurkProgram, "\n", "")
		lurkProgram = strings.ReplaceAll(lurkProgram, "\t", "")
		assert.NoError(t, err)
		assert.Truef(t, macros.IsValidLurk(lurkProgram), "Test %d should be valid", i)
		assert.Equalf(t, test.expected, lurkProgram, "Test %d not as expected", i)
	}
}

func TestCircularImports(t *testing.T) {
	mod1 := `!(module math (
			!(import utils)
			!(defun plus-three (x) (+ x 3))
		))

		!(module time (
			!(assert (<= !(param locktime-precision) 30))
		))
		`

	mod2 := `!(module utils (
			!(import math)
		))
		`

	tempDir := path.Join(os.TempDir(), "circular_import_test")
	defer os.Remove(tempDir)

	err := os.MkdirAll(tempDir, 0755)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "mod1.lurk"), []byte(mod1), 0644)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(tempDir, "mod2.lurk"), []byte(mod2), 0644)
	assert.NoError(t, err)

	mp, err := macros.NewMacroPreprocessor(macros.DependencyDir(tempDir))
	assert.NoError(t, err)

	lurkProgram := `!(defun my-func (y) (
				!(import math)
				(plus-two 10)
			))`

	_, err = mp.Preprocess(lurkProgram)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, macros.ErrCircularImports))
}

func TestWithStandardLib(t *testing.T) {
	mp, err := macros.NewMacroPreprocessor(macros.WithStandardLib(), macros.RemoveComments())
	assert.NoError(t, err)

	lurkProgram := `!(defun my-func (y) (
				!(import std/crypto/checksig)
				(checksig 10)
			))`
	lurkProgram, err = mp.Preprocess(lurkProgram)
	assert.NoError(t, err)
	lurkProgram = strings.ReplaceAll(lurkProgram, "\n", "")
	lurkProgram = strings.ReplaceAll(lurkProgram, "\t", "")
	lurkProgram = strings.Join(strings.Fields(lurkProgram), " ")
	assert.True(t, macros.IsValidLurk(lurkProgram))
	expected := `(letrec ((my-func (lambda (y) (letrec ((checksig (lambda (sig pubkey sighash) (eval (cons 'coproc_checksig (cons (car sig) (cons (car (cdr sig)) (cons (car (cdr (cdr sig))) (cons (car pubkey) (cons (car (cdr pubkey)) (cons sighash nil)))))))) )))(checksig 10))))))`
	assert.Equal(t, expected, lurkProgram)
}
