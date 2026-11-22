#import <UIKit/UIKit.h>
#import <ImageIO/ImageIO.h>
#import <TargetConditionals.h>

int shiojiIsTablet(void) {
    return UIDevice.currentDevice.userInterfaceIdiom == UIUserInterfaceIdiomPad;
}

int shiojiIsSimulator(void) {
    return TARGET_OS_SIMULATOR;
}

unsigned char *shiojiImageThumbnail(const char *path, int *width, int *height) {
    @autoreleasepool {
        NSURL *url = [NSURL fileURLWithPath:[NSString stringWithUTF8String:path]];
        NSDictionary *sourceOptions = @{(__bridge NSString *)kCGImageSourceShouldCache: @NO};
        CGImageSourceRef source = CGImageSourceCreateWithURL((__bridge CFURLRef)url, (__bridge CFDictionaryRef)sourceOptions);
        if (!source) {
            return NULL;
        }
        NSDictionary *options = @{
            (__bridge NSString *)kCGImageSourceCreateThumbnailFromImageAlways: @YES,
            (__bridge NSString *)kCGImageSourceCreateThumbnailWithTransform: @YES,
            (__bridge NSString *)kCGImageSourceThumbnailMaxPixelSize: @512
        };
        CGImageRef image = CGImageSourceCreateThumbnailAtIndex(source, 0, (__bridge CFDictionaryRef)options);
        CFRelease(source);
        if (!image) {
            return NULL;
        }
        size_t w = CGImageGetWidth(image), h = CGImageGetHeight(image);
        unsigned char *pixels = calloc(w * h, 4);
        CGColorSpaceRef space = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
        CGContextRef context = pixels ? CGBitmapContextCreate(pixels, w, h, 8, w * 4, space,
            kCGBitmapByteOrder32Big | kCGImageAlphaPremultipliedLast) : NULL;
        CGColorSpaceRelease(space);
        if (context) {
            CGContextDrawImage(context, CGRectMake(0, 0, w, h), image);
            CGContextRelease(context);
            *width = (int)w;
            *height = (int)h;
        } else {
            free(pixels);
            pixels = NULL;
        }
        CGImageRelease(image);
        return pixels;
    }
}
