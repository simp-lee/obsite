// Copyright 2016 ego authors
//
// Modified by the Obsite authors in 2026 to make the model immutable and
// retain only deterministic Han-span cutting.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package chinese

const hmmMinFloat = -3.14e100

type hmmModel struct {
	emissions map[byte]map[rune]float64
}

func newHMMModel() *hmmModel {
	return &hmmModel{emissions: loadDefaultEmissions()}
}

func (m *hmmModel) cut(runes []rune) []string {
	if m == nil || len(runes) == 0 {
		return nil
	}

	states := m.viterbi(runes)
	result := make([]string, 0, len(runes))
	begin, next := 0, 0
	for index, current := range runes {
		switch states[index] {
		case 'B':
			begin = index
		case 'E':
			result = append(result, string(runes[begin:index+1]))
			next = index + 1
		case 'S':
			result = append(result, string(current))
			next = index + 1
		}
	}
	if next < len(runes) {
		result = append(result, string(runes[next:]))
	}
	return result
}

func (m *hmmModel) viterbi(observations []rune) []byte {
	if m == nil || len(observations) == 0 {
		return nil
	}

	states := [...]byte{'B', 'M', 'E', 'S'}
	probabilities := make([]map[byte]float64, len(observations))
	probabilities[0] = make(map[byte]float64, len(states))
	paths := make(map[byte][]byte, len(states))
	for _, state := range states {
		probabilities[0][state] = m.emission(state, observations[0]) + hmmStartProbability(state)
		paths[state] = []byte{state}
	}

	for index := 1; index < len(observations); index++ {
		probabilities[index] = make(map[byte]float64, len(states))
		nextPaths := make(map[byte][]byte, len(states))
		for _, state := range states {
			bestProbability := 0.0
			var bestPrevious byte
			found := false
			for _, previous := range hmmPreviousStates(state) {
				candidate := probabilities[index-1][previous] + hmmTransitionProbability(previous, state) + m.emission(state, observations[index])
				if !found || candidate > bestProbability || (candidate == bestProbability && previous > bestPrevious) {
					bestProbability = candidate
					bestPrevious = previous
					found = true
				}
			}

			probabilities[index][state] = bestProbability
			path := make([]byte, len(paths[bestPrevious]), len(paths[bestPrevious])+1)
			copy(path, paths[bestPrevious])
			nextPaths[state] = append(path, state)
		}
		paths = nextPaths
	}

	last := len(observations) - 1
	endState := byte('E')
	if probabilities[last]['S'] >= probabilities[last]['E'] {
		endState = 'S'
	}
	return paths[endState]
}

func (m *hmmModel) emission(state byte, observation rune) float64 {
	if m != nil {
		if values := m.emissions[state]; values != nil {
			if probability, ok := values[observation]; ok {
				return probability
			}
		}
	}
	return hmmMinFloat
}

func hmmStartProbability(state byte) float64 {
	switch state {
	case 'B':
		return -0.26268660809250016
	case 'S':
		return -1.4652633398537678
	default:
		return hmmMinFloat
	}
}

func hmmPreviousStates(state byte) []byte {
	switch state {
	case 'B':
		return []byte{'E', 'S'}
	case 'M':
		return []byte{'M', 'B'}
	case 'S':
		return []byte{'S', 'E'}
	case 'E':
		return []byte{'B', 'M'}
	default:
		return nil
	}
}

func hmmTransitionProbability(from byte, to byte) float64 {
	switch {
	case from == 'B' && to == 'E':
		return -0.510825623765990
	case from == 'B' && to == 'M':
		return -0.916290731874155
	case from == 'E' && to == 'B':
		return -0.5897149736854513
	case from == 'E' && to == 'S':
		return -0.8085250474669937
	case from == 'M' && to == 'E':
		return -0.33344856811948514
	case from == 'M' && to == 'M':
		return -1.2603623820268226
	case from == 'S' && to == 'B':
		return -0.7211965654669841
	case from == 'S' && to == 'S':
		return -0.6658631448798212
	default:
		return hmmMinFloat
	}
}
