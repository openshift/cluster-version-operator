package updatestatus

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	clocktesting "k8s.io/utils/clock/testing"

	updatev1alpha1 "github.com/openshift/api/update/v1alpha1"
	fakeupdateclient "github.com/openshift/client-go/update/clientset/versioned/fake"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

var compareOnlyStatus = cmpopts.IgnoreFields(updatev1alpha1.UpdateStatus{}, "TypeMeta", "ObjectMeta", "Spec")

func Test_updateStatusController(t *testing.T) {
	var now = time.Now()
	var minus90sec = now.Add(-90 * time.Second)
	var minus30sec = now.Add(-30 * time.Second)
	var plus30sec = now.Add(30 * time.Second)
	var plus60sec = now.Add(1 * time.Minute)

	cvResourceRef := updatev1alpha1.ResourceRef{
		Group:    "config.openshift.io",
		Resource: "clusterversions",
		Name:     "version",
	}

	cvInsight := updatev1alpha1.ControlPlaneInsight{
		UID:        "cv-version",
		AcquiredAt: metav1.NewTime(now),
		ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
			Type: updatev1alpha1.ClusterVersionStatusInsightType,
			ClusterVersionStatusInsight: &updatev1alpha1.ClusterVersionStatusInsight{
				Resource: cvResourceRef,
			},
		},
	}

	coResourceRef := updatev1alpha1.ResourceRef{
		Group:    "config.openshift.io",
		Resource: "clusteroperators",
		Name:     "cluster-operator",
	}

	coInsight := updatev1alpha1.ControlPlaneInsight{
		UID:        "co-cluster-operator",
		AcquiredAt: metav1.NewTime(now),
		ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
			Type: updatev1alpha1.ClusterOperatorStatusInsightType,
			ClusterOperatorStatusInsight: &updatev1alpha1.ClusterOperatorStatusInsight{
				Resource: coResourceRef,
			},
		},
	}

	testCases := []struct {
		name string

		before *updateStatusApi

		informerMsg []informerMsg

		expected *updateStatusApi
	}{
		{
			name:        "no messages, no state -> no state",
			before:      &updateStatusApi{us: nil},
			informerMsg: []informerMsg{},
			expected:    &updateStatusApi{us: nil},
		},
		{
			name: "no messages, empty state -> empty state",
			before: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{},
			},
			expected: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{},
			},
		},
		{
			name: "no messages, state -> unchanged state",
			before: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{
					Status: updatev1alpha1.UpdateStatusStatus{
						ControlPlane: updatev1alpha1.ControlPlane{
							Informers: []updatev1alpha1.ControlPlaneInformer{
								{
									Name:     "cpi",
									Insights: []updatev1alpha1.ControlPlaneInsight{cvInsight},
								},
							},
						},
					},
				},
			},
			expected: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{
					Status: updatev1alpha1.UpdateStatusStatus{
						ControlPlane: updatev1alpha1.ControlPlane{
							Informers: []updatev1alpha1.ControlPlaneInformer{
								{
									Name:     "cpi",
									Insights: []updatev1alpha1.ControlPlaneInsight{cvInsight},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "one message, no state -> initialize from message",
			before: &updateStatusApi{
				us: nil,
			},
			informerMsg: []informerMsg{
				{
					informer:  "cpi",
					uid:       cvInsight.UID,
					cpInsight: &cvInsight,
				},
			},
			expected: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{
					ObjectMeta: metav1.ObjectMeta{Name: "status-api-prototype"},
					Status: updatev1alpha1.UpdateStatusStatus{
						ControlPlane: updatev1alpha1.ControlPlane{
							Resource: cvResourceRef,
							Informers: []updatev1alpha1.ControlPlaneInformer{
								{
									Name:     "cpi",
									Insights: []updatev1alpha1.ControlPlaneInsight{cvInsight},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "messages over time build state over old state",
			before: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{
					ObjectMeta: metav1.ObjectMeta{Name: "status-api-prototype"},
					Status: updatev1alpha1.UpdateStatusStatus{
						ControlPlane: updatev1alpha1.ControlPlane{
							Resource: cvResourceRef,
							Informers: []updatev1alpha1.ControlPlaneInformer{
								{
									Name: "cpi",
									Insights: []updatev1alpha1.ControlPlaneInsight{
										cvInsight,
										{
											UID:        "overwritten",
											AcquiredAt: metav1.NewTime(now),
											ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
												Type: updatev1alpha1.ClusterOperatorStatusInsightType,
												ClusterOperatorStatusInsight: &updatev1alpha1.ClusterOperatorStatusInsight{
													Conditions: []metav1.Condition{
														{
															Type:    string(updatev1alpha1.ClusterOperatorStatusInsightUpdating),
															Status:  metav1.ConditionFalse,
															Reason:  "Original",
															Message: "Original message",
														},
													},
													Name: "overwritten",
													Resource: updatev1alpha1.ResourceRef{
														Group:    "config.openshift.io",
														Resource: "clusteroperators",
														Name:     "overwritten",
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			informerMsg: []informerMsg{
				{
					informer: "cpi",
					uid:      "new-clusteroperator",
					cpInsight: &updatev1alpha1.ControlPlaneInsight{
						UID:        "new-clusteroperator",
						AcquiredAt: metav1.NewTime(now),
						ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
							Type: updatev1alpha1.ClusterOperatorStatusInsightType,
							ClusterOperatorStatusInsight: &updatev1alpha1.ClusterOperatorStatusInsight{
								Conditions: []metav1.Condition{
									{
										Type:    string(updatev1alpha1.ClusterOperatorStatusInsightUpdating),
										Status:  metav1.ConditionTrue,
										Reason:  "NewClusterOperator",
										Message: "Message about new ClusterOperator",
									},
								},
								Name: "new-clusteroperator",
								Resource: updatev1alpha1.ResourceRef{
									Group:    "config.openshift.io",
									Resource: "clusteroperators",
									Name:     "new-clusteroperator",
								},
							},
						},
					},
					knownInsights: []string{cvInsight.UID, "overwritten"},
				},
				{
					informer: "cpi",
					uid:      "overwritten",
					cpInsight: &updatev1alpha1.ControlPlaneInsight{UID: "overwritten",
						AcquiredAt: metav1.NewTime(now),
						ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
							Type: updatev1alpha1.ClusterOperatorStatusInsightType,
							ClusterOperatorStatusInsight: &updatev1alpha1.ClusterOperatorStatusInsight{
								Conditions: []metav1.Condition{
									{
										Type:    string(updatev1alpha1.ClusterOperatorStatusInsightUpdating),
										Status:  metav1.ConditionUnknown,
										Reason:  "FirstWrite",
										Message: "First update into overwritten CO",
									},
								},
								Name: "overwritten",
								Resource: updatev1alpha1.ResourceRef{
									Group:    "config.openshift.io",
									Resource: "clusteroperators",
									Name:     "overwritten",
								},
							},
						},
					},
					knownInsights: []string{cvInsight.UID, "new-clusteroperator"},
				},
				{
					informer: "cpi",
					uid:      "another-clusteroperator",
					cpInsight: &updatev1alpha1.ControlPlaneInsight{
						UID:        "another-clusteroperator",
						AcquiredAt: metav1.NewTime(now),
						ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
							Type: updatev1alpha1.ClusterOperatorStatusInsightType,
							ClusterOperatorStatusInsight: &updatev1alpha1.ClusterOperatorStatusInsight{
								Conditions: []metav1.Condition{
									{
										Type:    string(updatev1alpha1.ClusterOperatorStatusInsightUpdating),
										Status:  metav1.ConditionTrue,
										Reason:  "AnotherClusterOperator",
										Message: "Message about another ClusterOperator",
									},
								},
								Name: "another-clusteroperator",
								Resource: updatev1alpha1.ResourceRef{
									Group:    "config.openshift.io",
									Resource: "clusteroperators",
									Name:     "another-clusteroperator",
								},
							},
						},
					},
					knownInsights: []string{cvInsight.UID, "new-clusteroperator", "overwritten"},
				},
				{
					informer: "cpi",
					uid:      "overwritten",
					cpInsight: &updatev1alpha1.ControlPlaneInsight{UID: "overwritten",
						AcquiredAt: metav1.NewTime(now),
						ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
							Type: updatev1alpha1.ClusterOperatorStatusInsightType,
							ClusterOperatorStatusInsight: &updatev1alpha1.ClusterOperatorStatusInsight{
								Conditions: []metav1.Condition{
									{
										Type:    string(updatev1alpha1.ClusterOperatorStatusInsightUpdating),
										Status:  metav1.ConditionTrue,
										Reason:  "FinalWrite",
										Message: "Final update into overwritten CO",
									},
								},
								Name: "overwritten",
								Resource: updatev1alpha1.ResourceRef{
									Group:    "config.openshift.io",
									Resource: "clusteroperators",
									Name:     "overwritten",
								},
							},
						},
					},
					knownInsights: []string{cvInsight.UID, "new-clusteroperator", "another-clusteroperator"},
				},
			},
			expected: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{
					ObjectMeta: metav1.ObjectMeta{Name: "status-api-prototype"},
					Status: updatev1alpha1.UpdateStatusStatus{
						ControlPlane: updatev1alpha1.ControlPlane{
							Resource: cvResourceRef,
							Informers: []updatev1alpha1.ControlPlaneInformer{
								{
									Name: "cpi",
									Insights: []updatev1alpha1.ControlPlaneInsight{
										cvInsight,
										{
											UID:        "new-clusteroperator",
											AcquiredAt: metav1.NewTime(now),
											ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
												Type: updatev1alpha1.ClusterOperatorStatusInsightType,
												ClusterOperatorStatusInsight: &updatev1alpha1.ClusterOperatorStatusInsight{
													Conditions: []metav1.Condition{
														{
															Type:    string(updatev1alpha1.ClusterOperatorStatusInsightUpdating),
															Status:  metav1.ConditionTrue,
															Reason:  "NewClusterOperator",
															Message: "Message about new ClusterOperator",
														},
													},
													Name: "new-clusteroperator",
													Resource: updatev1alpha1.ResourceRef{
														Group:    "config.openshift.io",
														Resource: "clusteroperators",
														Name:     "new-clusteroperator",
													},
												},
											},
										},
										{
											UID:        "overwritten",
											AcquiredAt: metav1.NewTime(now),
											ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
												Type: updatev1alpha1.ClusterOperatorStatusInsightType,
												ClusterOperatorStatusInsight: &updatev1alpha1.ClusterOperatorStatusInsight{
													Conditions: []metav1.Condition{
														{
															Type:    string(updatev1alpha1.ClusterOperatorStatusInsightUpdating),
															Status:  metav1.ConditionTrue,
															Reason:  "FinalWrite",
															Message: "Final update into overwritten CO",
														},
													},
													Name: "overwritten",
													Resource: updatev1alpha1.ResourceRef{
														Group:    "config.openshift.io",
														Resource: "clusteroperators",
														Name:     "overwritten",
													},
												},
											},
										},
										{
											UID:        "another-clusteroperator",
											AcquiredAt: metav1.NewTime(now),
											ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
												Type: updatev1alpha1.ClusterOperatorStatusInsightType,
												ClusterOperatorStatusInsight: &updatev1alpha1.ClusterOperatorStatusInsight{
													Conditions: []metav1.Condition{
														{
															Type:    string(updatev1alpha1.ClusterOperatorStatusInsightUpdating),
															Status:  metav1.ConditionTrue,
															Reason:  "AnotherClusterOperator",
															Message: "Message about another ClusterOperator",
														},
													},
													Name: "another-clusteroperator",
													Resource: updatev1alpha1.ResourceRef{
														Group:    "config.openshift.io",
														Resource: "clusteroperators",
														Name:     "another-clusteroperator",
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:   "messages can come from different informers",
			before: &updateStatusApi{},
			informerMsg: []informerMsg{
				{
					informer: "one",
					uid:      "item",
					cpInsight: &updatev1alpha1.ControlPlaneInsight{
						UID:        "item",
						AcquiredAt: metav1.NewTime(now),
						ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
							Type: updatev1alpha1.HealthInsightType,
							HealthInsight: &updatev1alpha1.HealthInsight{
								StartedAt: metav1.NewTime(minus30sec),
								Scope: updatev1alpha1.InsightScope{
									Type:      updatev1alpha1.ControlPlaneScope,
									Resources: []updatev1alpha1.ResourceRef{cvResourceRef},
								},
								Impact: updatev1alpha1.InsightImpact{
									Level:       updatev1alpha1.InfoImpactLevel,
									Type:        updatev1alpha1.UnknownImpactType,
									Summary:     "Item from informer one",
									Description: "Longer description about item from informer one",
								},
								Remediation: updatev1alpha1.InsightRemediation{Reference: "https://example.com"},
							},
						},
					},
				},
				{
					informer: "two",
					uid:      "item",
					cpInsight: &updatev1alpha1.ControlPlaneInsight{
						UID:        "item",
						AcquiredAt: metav1.NewTime(now),
						ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
							Type: updatev1alpha1.HealthInsightType,
							HealthInsight: &updatev1alpha1.HealthInsight{
								StartedAt: metav1.NewTime(minus90sec),
								Scope: updatev1alpha1.InsightScope{
									Type:      updatev1alpha1.ControlPlaneScope,
									Resources: []updatev1alpha1.ResourceRef{cvResourceRef},
								},
								Impact: updatev1alpha1.InsightImpact{
									Level:       updatev1alpha1.InfoImpactLevel,
									Type:        updatev1alpha1.UnknownImpactType,
									Summary:     "Item from informer two",
									Description: "Longer description about item from informer two",
								},
								Remediation: updatev1alpha1.InsightRemediation{Reference: "https://example.com"},
							},
						},
					},
				},
				{
					informer: "three",
					uid:      "item",
					cpInsight: &updatev1alpha1.ControlPlaneInsight{
						UID:        "item",
						AcquiredAt: metav1.NewTime(now),
						ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
							Type: updatev1alpha1.HealthInsightType,
							HealthInsight: &updatev1alpha1.HealthInsight{
								StartedAt: metav1.NewTime(minus90sec),
								Scope: updatev1alpha1.InsightScope{
									Type:      updatev1alpha1.ControlPlaneScope,
									Resources: []updatev1alpha1.ResourceRef{cvResourceRef},
								},
								Impact: updatev1alpha1.InsightImpact{
									Level:       updatev1alpha1.InfoImpactLevel,
									Type:        updatev1alpha1.UnknownImpactType,
									Summary:     "Item from informer three",
									Description: "Longer description about item from informer three",
								},
								Remediation: updatev1alpha1.InsightRemediation{Reference: "https://example.com"},
							},
						},
					},
				},
			},
			expected: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{
					ObjectMeta: metav1.ObjectMeta{Name: "status-api-prototype"},
					Status: updatev1alpha1.UpdateStatusStatus{
						ControlPlane: updatev1alpha1.ControlPlane{
							Informers: []updatev1alpha1.ControlPlaneInformer{
								{
									Name: "one",
									Insights: []updatev1alpha1.ControlPlaneInsight{
										{
											UID:        "item",
											AcquiredAt: metav1.NewTime(now),
											ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
												Type: updatev1alpha1.HealthInsightType,
												HealthInsight: &updatev1alpha1.HealthInsight{
													StartedAt: metav1.NewTime(minus30sec),
													Scope: updatev1alpha1.InsightScope{
														Type:      updatev1alpha1.ControlPlaneScope,
														Resources: []updatev1alpha1.ResourceRef{cvResourceRef},
													},
													Impact: updatev1alpha1.InsightImpact{
														Level:       updatev1alpha1.InfoImpactLevel,
														Type:        updatev1alpha1.UnknownImpactType,
														Summary:     "Item from informer one",
														Description: "Longer description about item from informer one",
													},
													Remediation: updatev1alpha1.InsightRemediation{Reference: "https://example.com"},
												},
											},
										},
									},
								},
								{
									Name: "two",
									Insights: []updatev1alpha1.ControlPlaneInsight{
										{
											UID:        "item",
											AcquiredAt: metav1.NewTime(now),
											ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
												Type: updatev1alpha1.HealthInsightType,
												HealthInsight: &updatev1alpha1.HealthInsight{
													StartedAt: metav1.NewTime(minus90sec),
													Scope: updatev1alpha1.InsightScope{
														Type:      updatev1alpha1.ControlPlaneScope,
														Resources: []updatev1alpha1.ResourceRef{cvResourceRef},
													},
													Impact: updatev1alpha1.InsightImpact{
														Level:       updatev1alpha1.InfoImpactLevel,
														Type:        updatev1alpha1.UnknownImpactType,
														Summary:     "Item from informer two",
														Description: "Longer description about item from informer two",
													},
													Remediation: updatev1alpha1.InsightRemediation{Reference: "https://example.com"},
												},
											},
										},
									},
								},
								{
									Name: "three",
									Insights: []updatev1alpha1.ControlPlaneInsight{
										{
											UID:        "item",
											AcquiredAt: metav1.NewTime(now),
											ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
												Type: updatev1alpha1.HealthInsightType,
												HealthInsight: &updatev1alpha1.HealthInsight{
													StartedAt: metav1.NewTime(minus90sec),
													Scope: updatev1alpha1.InsightScope{
														Type:      updatev1alpha1.ControlPlaneScope,
														Resources: []updatev1alpha1.ResourceRef{cvResourceRef},
													},
													Impact: updatev1alpha1.InsightImpact{
														Level:       updatev1alpha1.InfoImpactLevel,
														Type:        updatev1alpha1.UnknownImpactType,
														Summary:     "Item from informer three",
														Description: "Longer description about item from informer three",
													},
													Remediation: updatev1alpha1.InsightRemediation{Reference: "https://example.com"},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:   "empty informer -> message gets dropped",
			before: &updateStatusApi{us: nil},
			informerMsg: []informerMsg{
				{
					informer:  "",
					uid:       "item",
					cpInsight: &cvInsight,
				},
			},
			expected: &updateStatusApi{us: nil},
		},
		{
			name:   "empty uid -> message gets dropped",
			before: &updateStatusApi{us: nil},
			informerMsg: []informerMsg{
				{
					informer:  "one",
					uid:       "",
					cpInsight: &cvInsight,
				},
			},
			expected: &updateStatusApi{us: nil},
		},
		{
			name:   "nil insight payload -> message gets dropped",
			before: &updateStatusApi{us: nil},
			informerMsg: []informerMsg{
				{
					informer:  "one",
					uid:       "item",
					cpInsight: nil,
					wpInsight: nil,
				},
			},
			expected: &updateStatusApi{us: nil},
		},
		{
			name:   "both cp & wp insights payload -> message gets dropped",
			before: &updateStatusApi{us: nil},
			informerMsg: []informerMsg{
				{
					informer:  "one",
					uid:       "item",
					cpInsight: &updatev1alpha1.ControlPlaneInsight{UID: "item"},
					wpInsight: &updatev1alpha1.WorkerPoolInsight{UID: "item"},
				},
			},
			expected: &updateStatusApi{us: nil},
		},
		{
			name: "unknown insight -> not removed from state immediately but set for expiration",
			before: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{
					ObjectMeta: metav1.ObjectMeta{Name: "status-api-prototype"},
					Status: updatev1alpha1.UpdateStatusStatus{
						ControlPlane: updatev1alpha1.ControlPlane{
							Informers: []updatev1alpha1.ControlPlaneInformer{
								{
									Name: "one",
									Insights: []updatev1alpha1.ControlPlaneInsight{
										coInsight,
									},
								},
							},
						},
					},
				},
			},
			informerMsg: []informerMsg{{
				informer:      "one",
				uid:           cvInsight.UID,
				cpInsight:     &cvInsight,
				knownInsights: nil,
			}},
			expected: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{
					ObjectMeta: metav1.ObjectMeta{Name: "status-api-prototype"},
					Status: updatev1alpha1.UpdateStatusStatus{
						ControlPlane: updatev1alpha1.ControlPlane{
							Resource: cvResourceRef,
							Informers: []updatev1alpha1.ControlPlaneInformer{
								{
									Name: "one",
									Insights: []updatev1alpha1.ControlPlaneInsight{
										coInsight,
										cvInsight,
									},
								},
							},
						},
					},
				},
				unknownInsightExpirations: map[string]insightExpirations{
					"one": {coInsight.UID: plus60sec},
				},
			},
		},
		{
			name: "unknown insight already set for expiration -> not removed from state while not expired yet",
			before: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{
					ObjectMeta: metav1.ObjectMeta{Name: "status-api-prototype"},
					Status: updatev1alpha1.UpdateStatusStatus{
						ControlPlane: updatev1alpha1.ControlPlane{
							Informers: []updatev1alpha1.ControlPlaneInformer{
								{
									Name: "one",
									Insights: []updatev1alpha1.ControlPlaneInsight{
										coInsight,
									},
								},
							},
						},
					},
				},
				unknownInsightExpirations: map[string]insightExpirations{
					"one": {coInsight.UID: plus30sec},
				},
			},
			informerMsg: []informerMsg{{
				informer:      "one",
				uid:           cvInsight.UID,
				cpInsight:     &cvInsight,
				knownInsights: nil,
			}},
			expected: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{
					ObjectMeta: metav1.ObjectMeta{Name: "status-api-prototype"},
					Status: updatev1alpha1.UpdateStatusStatus{
						ControlPlane: updatev1alpha1.ControlPlane{
							Resource: cvResourceRef,
							Informers: []updatev1alpha1.ControlPlaneInformer{
								{
									Name: "one",
									Insights: []updatev1alpha1.ControlPlaneInsight{
										coInsight,
										cvInsight,
									},
								},
							},
						},
					},
				},
				unknownInsightExpirations: map[string]insightExpirations{
					"one": {coInsight.UID: plus30sec},
				},
			},
		},
		{
			name: "previously unknown insight set for expiration is known again -> kept in state and expire dropped",
			before: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{
					ObjectMeta: metav1.ObjectMeta{Name: "status-api-prototype"},
					Status: updatev1alpha1.UpdateStatusStatus{
						ControlPlane: updatev1alpha1.ControlPlane{
							Informers: []updatev1alpha1.ControlPlaneInformer{
								{
									Name: "one",
									Insights: []updatev1alpha1.ControlPlaneInsight{
										coInsight,
									},
								},
							},
						},
					},
				},
				unknownInsightExpirations: map[string]insightExpirations{
					"one": {coInsight.UID: minus30sec},
				},
			},
			informerMsg: []informerMsg{{
				informer:      "one",
				uid:           cvInsight.UID,
				cpInsight:     &cvInsight,
				knownInsights: []string{coInsight.UID},
			}},
			expected: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{
					ObjectMeta: metav1.ObjectMeta{Name: "status-api-prototype"},
					Status: updatev1alpha1.UpdateStatusStatus{
						ControlPlane: updatev1alpha1.ControlPlane{
							Resource: cvResourceRef,
							Informers: []updatev1alpha1.ControlPlaneInformer{
								{
									Name: "one",
									Insights: []updatev1alpha1.ControlPlaneInsight{
										coInsight,
										cvInsight,
									},
								},
							},
						},
					},
				},
				unknownInsightExpirations: nil,
			},
		},
		{
			name: "previously unknown insight expired and never became known again -> dropped from state and expire dropped",
			before: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{
					ObjectMeta: metav1.ObjectMeta{Name: "status-api-prototype"},
					Status: updatev1alpha1.UpdateStatusStatus{
						ControlPlane: updatev1alpha1.ControlPlane{
							Informers: []updatev1alpha1.ControlPlaneInformer{
								{
									Name: "one",
									Insights: []updatev1alpha1.ControlPlaneInsight{
										coInsight,
									},
								},
							},
						},
					},
				},
				unknownInsightExpirations: map[string]insightExpirations{
					"one": {coInsight.UID: minus90sec},
				},
			},
			informerMsg: []informerMsg{{
				informer:      "one",
				uid:           cvInsight.UID,
				cpInsight:     &cvInsight,
				knownInsights: nil,
			}},
			expected: &updateStatusApi{
				us: &updatev1alpha1.UpdateStatus{
					ObjectMeta: metav1.ObjectMeta{Name: "status-api-prototype"},
					Status: updatev1alpha1.UpdateStatusStatus{
						ControlPlane: updatev1alpha1.ControlPlane{
							Resource: cvResourceRef,
							Informers: []updatev1alpha1.ControlPlaneInformer{
								{
									Name: "one",
									Insights: []updatev1alpha1.ControlPlaneInsight{
										cvInsight,
									},
								},
							},
						},
					},
				},
				unknownInsightExpirations: nil,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			updateClient := fakeupdateclient.NewClientset()

			controller := updateStatusController{
				updateStatuses: updateClient.UpdateV1alpha1().UpdateStatuses(),
				state: updateStatusApi{
					us:                        tc.before.us,
					unknownInsightExpirations: tc.before.unknownInsightExpirations,
					now:                       func() time.Time { return now },
				},
			}

			startInsightReceiver, sendInsight := controller.setupInsightReceiver()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			go func() {
				_ = startInsightReceiver(ctx, newTestSyncContextWithQueue())
			}()

			for _, msg := range tc.informerMsg {
				sendInsight(msg)
			}

			expectedProcessed := len(tc.informerMsg)
			var sawProcessed int
			var diffConfigMap string
			var diffExpirations string

			tc.expected.sort()

			backoff := wait.Backoff{Duration: 5 * time.Millisecond, Factor: 2, Steps: 10}
			if err := wait.ExponentialBackoff(backoff, func() (bool, error) {
				controller.state.Lock()
				defer controller.state.Unlock()

				controller.state.sort()

				sawProcessed = controller.state.processed
				diffConfigMap = cmp.Diff(tc.expected.us, controller.state.us, compareOnlyStatus)
				diffExpirations = cmp.Diff(tc.expected.unknownInsightExpirations, controller.state.unknownInsightExpirations)

				return diffConfigMap == "" && diffExpirations == "" && sawProcessed == expectedProcessed, nil
			}); err != nil {
				if diffConfigMap != "" {
					t.Errorf("controller config map differs from expected:\n%s", diffConfigMap)
				}
				if diffExpirations != "" {
					t.Errorf("expirations differ from expected:\n%s", diffExpirations)
				}
				if controller.state.processed != len(tc.informerMsg) {
					t.Errorf("controller processed %d messages, expected %d", controller.state.processed, len(tc.informerMsg))
				}
			}
		})
	}
}

func newTestSyncContextWithQueue() factory.SyncContext {
	return testSyncContext{
		eventRecorder: events.NewInMemoryRecorder("test", clocktesting.NewFakePassiveClock(time.Now())),
		queue:         workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[any]()),
	}
}
