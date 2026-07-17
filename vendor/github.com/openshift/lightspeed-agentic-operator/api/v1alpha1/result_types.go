/*
Copyright 2026.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ResultConditionStarted   = "Started"
	ResultConditionCompleted = "Completed"

	ResultReasonStepStarted = "StepStarted"
	ResultReasonSucceeded   = "Succeeded"
	ResultReasonFailed      = "Failed"
)

func (r *AnalysisResult) GetConditions() []metav1.Condition      { return r.Status.Conditions }
func (r *AnalysisResult) SetConditions(c []metav1.Condition)      { r.Status.Conditions = c }
func (r *ExecutionResult) GetConditions() []metav1.Condition      { return r.Status.Conditions }
func (r *ExecutionResult) SetConditions(c []metav1.Condition)      { r.Status.Conditions = c }
func (r *VerificationResult) GetConditions() []metav1.Condition   { return r.Status.Conditions }
func (r *VerificationResult) SetConditions(c []metav1.Condition)   { r.Status.Conditions = c }
func (r *EscalationResult) GetConditions() []metav1.Condition     { return r.Status.Conditions }
func (r *EscalationResult) SetConditions(c []metav1.Condition)     { r.Status.Conditions = c }
