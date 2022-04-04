package wfc

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math/rand"
)

var (
	ErrNoSolution = errors.New("no possible modules for slot")
)

// Wave holds the state of a wave collapse function as described by Oskar
// Stalberg.
//
// The wave is a recursive algorithm that collapses the possibility space of a
// 2D grid into a single output. Specifically, it is a 2D array of slots that
// are all in a superposition state of one or more modules; each module is a
// possible tile that might exist at that slot.
//
// The algorithm is described in detail by Oskar Stalberg at several
// conferences. It is described in detail during the following talk:
// https://www.youtube.com/watch?v=0bcZb-SsnrA&t=350s
//
// An example implmentation of the algorithm can be found here:
// https://oskarstalberg.com/game/wave/wave.html
type Wave struct {
	Width, Height    int       // Width and height of the grid
	Input            []*Module // Input tiles (possible tiles at each slot)
	PossibilitySpace []*Slot   // The 2D grid of slots

	History []*Slot // Slots that have been visited during the current/last collapse iteration

	// Override this if you'd like custom logic when checking if a state is
	// possible from a direction. This is useful if you'd like to slow down the
	// collapse or add probabilities.
	IsPossibleFn IsPossibleFunc
}

// New creates a new wave collapse function with the given width and height and
// possible image tiles.
//
// Constraints are automatically generated by looking at the color values of the
// pixels along each of the four edges of the tile. Any tiles that should
// potentially be neighbors should have the same color values along the edges.
// Otherwise, the tile will not be considered a neighbor.
//
// When generating constraints, only 3 pixels per edge are considered. For
// example, for the top edge, it looks at the top-left, then top-middle, then
// top-right pixels. likewise for the right edge, it would look at the
// top-right, then middle-right, then bottom-right.
func New(tiles []image.Image, width, height int) *Wave {
	return NewWithCustomConstraints(tiles, width, height, DefaultConstraintFunc)
}

// NewWithCustomConstraints creates a new wave collapse function with the given
// adjacency constraint calculation function. Use this if you'd like custom
// logic for specifing constraints.
func NewWithCustomConstraints(tiles []image.Image, width, height int, fn ConstraintFunc) *Wave {
	wave := &Wave{
		Width:  width,
		Height: height,
		Input:  make([]*Module, len(tiles)),

		IsPossibleFn: DefaultIsPossibleFunc,
	}

	// Automatically generate adjacency constraints for each input tile.
	for i, tile := range tiles {
		module := Module{Image: tile}
		for _, d := range Directions {
			module.Adjacencies[d] = fn(tile, d)
		}
		wave.Input[i] = &module
	}

	return wave
}

// Initialize sets up the wave collapse function so that every slot is in a
// superposition of all input tiles/modules.
//
// Each module is equally likely to be at each slot.
func (w *Wave) Initialize(seed int) {
	rand.Seed(int64(seed)) // TODO: move off rand... this isn't thread safe; we can do better :)

	w.PossibilitySpace = make([]*Slot, w.Width*w.Height)
	for x := 0; x < w.Width; x++ {
		for y := 0; y < w.Height; y++ {
			slot := Slot{
				X: x, Y: y,
				Superposition: make([]*Module, len(w.Input)),
			}
			copy(slot.Superposition, w.Input)
			w.PossibilitySpace[x+y*w.Width] = &slot
		}
	}
}

// Collapse recursively collapses the possibility space for each slot into a
// single module.
//
// Important: Not all tile sets will allways produce a solution, so this
// function can return an error if a contradiction is found. You can still
// export the image of a failed collapse to see which of your tiles is causing
// issues for you.
func (w *Wave) Collapse(attempts int) error {

	for i := 0; i < attempts; i++ {
		err := w.Recurse()
		if err != nil {
			return err
		}
		w.History = make([]*Slot, 0)
	}

	return nil
}

// CollapseRandomSlot takes a random slot and collapses it into a single module.
// If the slot is already collapsed, it will pick another slot and try again.
func (w *Wave) CollapseRandomSlot() *Slot {
	num_collapsed := 0
	for _, s := range w.PossibilitySpace {
		entropy := len(s.Superposition)
		if entropy <= 1 {
			num_collapsed++
		}
	}

	// If all slots are already collapsed, we're done.
	if num_collapsed == len(w.PossibilitySpace) {
		return nil
	}

	// Pick a random slot that is not collapsed.
	for {
		slot := w.PossibilitySpace[rand.Intn(len(w.PossibilitySpace))]

		if len(slot.Superposition) <= 1 {
			continue
		}

		slot.Collapse()

		return slot
	}
}

// Recurse collapses the wave collapse function recursively.
func (w *Wave) Recurse() error {
	if w.IsCollapsed() {
		return nil
	}

	// Check if we need to pick a starting point
	if len(w.History) == 0 {
		slot := w.CollapseRandomSlot()
		w.History = append(w.History, slot)
	}

	previous := w.History[len(w.History)-1]
	for _, d := range Directions {
		if !w.HasNeighbor(previous, d) {
			continue
		}

		next := w.GetNeighbor(previous, d)
		if w.HasVisited(next) {
			continue
		}

		s := w.GetPossibleModules(previous, next, d)
		if len(s) == len(next.Superposition) {
			// Same state as before, no reason to recurse further
			continue
		} else {
			// New superposition detected, we need to go deeper and remove
			// impossible modules from the neighbor tiles
			next.Superposition = s
		}

		// Check if we have a contradiction
		if len(next.Superposition) == 0 {
			return ErrNoSolution
		}

		w.History = append(w.History, next)
		err := w.Recurse()
		if err != nil {
			return err
		}
		w.History = w.History[:len(w.History)-1]
	}

	return nil
}

// GetPossibleModules returns a list of modules that are possible when traveling
// from slot "a" to slot "b" with the provided direction.
func (w *Wave) GetPossibleModules(a, b *Slot, d Direction) []*Module {
	res := make([]*Module, 0)
	for _, m := range b.Superposition {
		if w.IsPossibleFn(m, a, b, d) {
			res = append(res, m)
		}
	}

	// Slot "a" has a state that does not allow any of the modules in slot "b".
	return res
}

// GetSlot returns the slot at the given coordinates in this wave function.
func (w *Wave) GetSlot(x, y int) *Slot {
	return w.PossibilitySpace[x+y*w.Width]
}

// HasVisited checks if the given slot has been visited during the current
// collapse iteration. This is used to prevent infinite recursion.
func (w *Wave) HasVisited(s *Slot) bool {
	for _, h := range w.History {
		if h == s {
			return true
		}
	}
	return false
}

// IsCollapsed checks if the given slot is collapsed. Either in a contradiction
// state or to a single possible value.
func (w *Wave) IsCollapsed() bool {
	for _, s := range w.PossibilitySpace {
		if len(s.Superposition) > 1 {
			return false
		}
	}
	return true
}

// HasNeighbor checks if the given slot has a neighbor in the given direction
// (edges of the grid don't have neighbors).
func (w *Wave) HasNeighbor(s *Slot, d Direction) bool {
	switch d {
	case Up:
		return s.Y > 0
	case Down:
		return s.Y < w.Height-1
	case Left:
		return s.X > 0
	case Right:
		return s.X < w.Width-1
	}
	return false
}

// GetNeighbor returns the slot in the given direction from the given slot.
func (w *Wave) GetNeighbor(s *Slot, d Direction) *Slot {
	switch d {
	case Up:
		return w.GetSlot(s.X, s.Y-1)
	case Down:
		return w.GetSlot(s.X, s.Y+1)
	case Left:
		return w.GetSlot(s.X-1, s.Y)
	case Right:
		return w.GetSlot(s.X+1, s.Y)
	}
	return nil
}

// Export takes the current state of the wave collapse function and exports it
// as an image. Any slots that have not been collapsed will be transparent.
// Contradictions will be red.
func (w *Wave) ExportImage() image.Image {
	u := w.Input[0].Image.Bounds().Max.X
	v := w.Input[0].Image.Bounds().Max.Y
	img := image.NewRGBA(image.Rect(0, 0, w.Width*u, w.Height*v))

	for _, s := range w.PossibilitySpace {
		if len(s.Superposition) == 1 {
			draw.Draw(img,
				image.Rect(s.X*u, s.Y*v, (s.X+1)*u, (s.Y+1)*v),
				s.Superposition[0].Image, image.ZP, draw.Over)
		}
		if len(s.Superposition) == 0 {
			c := color.RGBA{255, 0, 0, 255}
			for x := s.X * u; x < (s.X+1)*u; x++ {
				for y := s.Y * v; y < (s.Y+1)*v; y++ {
					img.Set(x, y, c)
				}
			}
		}
	}

	return img
}
