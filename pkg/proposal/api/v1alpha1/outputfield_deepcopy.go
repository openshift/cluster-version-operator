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

// Manual DeepCopy for output field types.
// controller-gen does not reliably generate deepcopy for these types
// when scanning multiple package paths simultaneously.

func (in *OutputField) DeepCopyInto(out *OutputField) {
	*out = *in
	if in.Enum != nil {
		in, out := &in.Enum, &out.Enum
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = new(OutputFieldItems)
		(*in).DeepCopyInto(*out)
	}
	if in.Properties != nil {
		in, out := &in.Properties, &out.Properties
		*out = make([]OutputSubField, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

func (in *OutputField) DeepCopy() *OutputField {
	if in == nil {
		return nil
	}
	out := new(OutputField)
	in.DeepCopyInto(out)
	return out
}

func (in *OutputSubField) DeepCopyInto(out *OutputSubField) {
	*out = *in
	if in.Enum != nil {
		in, out := &in.Enum, &out.Enum
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = new(OutputSubFieldItems)
		**out = **in
	}
}

func (in *OutputSubField) DeepCopy() *OutputSubField {
	if in == nil {
		return nil
	}
	out := new(OutputSubField)
	in.DeepCopyInto(out)
	return out
}

func (in *OutputFieldItems) DeepCopyInto(out *OutputFieldItems) {
	*out = *in
	if in.Properties != nil {
		in, out := &in.Properties, &out.Properties
		*out = make([]OutputSubField, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

func (in *OutputFieldItems) DeepCopy() *OutputFieldItems {
	if in == nil {
		return nil
	}
	out := new(OutputFieldItems)
	in.DeepCopyInto(out)
	return out
}

func (in *OutputSubFieldItems) DeepCopyInto(out *OutputSubFieldItems) {
	*out = *in
}

func (in *OutputSubFieldItems) DeepCopy() *OutputSubFieldItems {
	if in == nil {
		return nil
	}
	out := new(OutputSubFieldItems)
	in.DeepCopyInto(out)
	return out
}
