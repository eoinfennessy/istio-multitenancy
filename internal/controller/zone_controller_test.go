/*
Copyright 2024.

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

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"istio.io/api/annotation"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/eoinfennessy/istio-multitenancy/api/v1alpha1"
	"github.com/eoinfennessy/istio-multitenancy/pkg/constants"
)

var _ = Describe("Zone Controller", Ordered, func() {
	ctx := context.Background()

	blueZoneNamespaces := []string{"blue-a", "blue-b"}
	redZoneNamespaces := []string{"red-a", "red-b"}
	allNamespaces := append(blueZoneNamespaces, redZoneNamespaces...)

	SetDefaultEventuallyTimeout(10 * time.Second)

	BeforeAll(func() {
		By("creating namespaces")
		for _, ns := range allNamespaces {
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: ns,
				},
			}
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		}
	})

	AfterAll(func() {
		By("deleting namespaces")
		for _, ns := range allNamespaces {
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: ns,
				},
			}
			Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
		}
	})

	Context("creating a Zone", func() {
		const blueZoneName = "blue-zone"
		zoneKey := types.NamespacedName{Name: blueZoneName}
		zone := &v1alpha1.Zone{}

		BeforeAll(func() {
			By("creating the Zone resource")
			zone = &v1alpha1.Zone{
				ObjectMeta: metav1.ObjectMeta{
					Name: blueZoneName,
				},
				Spec: v1alpha1.ZoneSpec{
					Namespaces: blueZoneNamespaces,
				},
			}
			Expect(k8sClient.Create(ctx, zone)).To(Succeed())
		})

		AfterAll(func() {
			By("deleting the Zone resource")
			Expect(k8sClient.Delete(ctx, zone)).To(Succeed())
			Eventually(k8sClient.Get).WithArguments(ctx, zoneKey, zone).Should(MatchError(errors.IsNotFound, "IsNotFound"))
		})

		When("reconciling Services in the Zone", func() {
			var blueZoneServices []corev1.Service
			const svcCountPerNS = 2

			BeforeAll(func() {
				By("creating Services in the blue zone")
				blueZoneServices = make([]corev1.Service, 0, len(blueZoneNamespaces)*svcCountPerNS)
				for _, ns := range blueZoneNamespaces {
					for svcNumber := range svcCountPerNS {
						svc := corev1.Service{
							ObjectMeta: metav1.ObjectMeta{
								Name:      fmt.Sprintf("%s-%d", ns, svcNumber),
								Namespace: ns,
							},
							Spec: corev1.ServiceSpec{
								Ports: []corev1.ServicePort{
									{Port: 8080},
								},
							},
						}
						blueZoneServices = append(blueZoneServices, svc)
						Expect(k8sClient.Create(ctx, &svc)).To(Succeed())
					}
				}
			})

			AfterAll(func() {
				By("cleaning up Services in the blue zone")
				for _, svc := range blueZoneServices {
					Expect(k8sClient.Delete(ctx, &svc)).To(Succeed())
					svcKey := types.NamespacedName{Name: svc.GetName(), Namespace: svc.GetNamespace()}
					Eventually(k8sClient.Get).WithArguments(ctx, svcKey, &svc).Should(MatchError(errors.IsNotFound, "IsNotFound"))
				}
			})

			It("should successfully reconcile the Zone", func() {
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, zoneKey, zone)).To(Succeed())
					reconciledCondition := zone.Status.FindStatusCondition(v1alpha1.ConditionTypeReconciled)
					g.Expect(reconciledCondition).NotTo(BeNil())
					g.Expect(reconciledCondition.Status).To(Equal(metav1.ConditionTrue))
				}).Should(Succeed())
			})

			It("should add the appropriate label to Services in the Zone", func() {
				Eventually(func(g Gomega) {
					for _, svc := range blueZoneServices {
						svcKey := types.NamespacedName{Name: svc.GetName(), Namespace: svc.GetNamespace()}
						s := &corev1.Service{}
						g.Expect(k8sClient.Get(ctx, svcKey, s)).To(Succeed())

						labelValue, labelExists := s.GetLabels()[constants.ZoneLabel]
						g.Expect(labelExists).To(BeTrue())
						g.Expect(labelValue).To(Equal(blueZoneName))
					}
				}).Should(Succeed())
			})

			It("should add the appropriate annotation to Services in the Zone", func() {
				Eventually(func(g Gomega) {
					for _, svc := range blueZoneServices {
						svcKey := types.NamespacedName{Name: svc.GetName(), Namespace: svc.GetNamespace()}
						s := &corev1.Service{}
						g.Expect(k8sClient.Get(ctx, svcKey, s)).To(Succeed())

						annotationValue, annotationExists := s.GetAnnotations()[annotation.NetworkingExportTo.Name]
						g.Expect(annotationExists).To(BeTrue())
						expectedAnnotationValue := strings.Join(blueZoneNamespaces, ",")
						g.Expect(annotationValue).To(Equal(expectedAnnotationValue))
					}
				}).Should(Succeed())
			})

			When("removing a namespace from a Zone's from spec", func() {
				var removedNamespace = blueZoneNamespaces[0]

				BeforeAll(func() {
					By("updating the Zone resource")
					zone.Spec.Namespaces = blueZoneNamespaces[1:]
					Expect(k8sClient.Update(ctx, zone)).To(Succeed())
				})

				AfterAll(func() {
					By("re-adding the removed namespace to the Zone resource")
					zone.Spec.Namespaces = blueZoneNamespaces
					Expect(k8sClient.Update(ctx, zone)).To(Succeed())
				})

				It("should update the value of the exportTo annotation on Services that remain in the Zone", func() {
					Eventually(func(g Gomega) {
						for _, svc := range blueZoneServices {
							if svc.GetNamespace() == removedNamespace {
								continue
							}

							svcKey := types.NamespacedName{Name: svc.GetName(), Namespace: svc.GetNamespace()}
							s := &corev1.Service{}
							g.Expect(k8sClient.Get(ctx, svcKey, s)).To(Succeed())

							annotationValue, annotationExists := s.GetAnnotations()[annotation.NetworkingExportTo.Name]
							g.Expect(annotationExists).To(BeTrue())
							expectedAnnotationValue := strings.Join(blueZoneNamespaces[1:], ",")
							g.Expect(annotationValue).To(Equal(expectedAnnotationValue))
						}
					}).Should(Succeed())
				})

				It("should remove annotations and labels from Services that were previously in the Zone", func() {
					Eventually(func(g Gomega) {
						for _, svc := range blueZoneServices {
							if svc.GetNamespace() != removedNamespace {
								continue
							}

							svcKey := types.NamespacedName{Name: svc.GetName(), Namespace: svc.GetNamespace()}
							s := &corev1.Service{}
							g.Expect(k8sClient.Get(ctx, svcKey, s)).To(Succeed())

							_, annotationExists := s.GetAnnotations()[annotation.NetworkingExportTo.Name]
							g.Expect(annotationExists).To(BeFalse())
							_, labelExists := s.GetLabels()[constants.ZoneLabel]
							g.Expect(labelExists).To(BeFalse())
						}
					}).Should(Succeed())
				})
			})
		})
	})
})
