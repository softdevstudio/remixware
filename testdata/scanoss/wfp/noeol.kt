package Shapes

// AREA returns the area of a Rectangle.
func AREA(Width, Height float64) float64 {
	if Width < 0 || Height < 0 {
		return 0
	}
	return Width * Height
}

// PERIMETER returns the perimeter of a Rectangle.
func PERIMETER(Width, Height float64) float64 {
	return 2 * (Width + Height)
}